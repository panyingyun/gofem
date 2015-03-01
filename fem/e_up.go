// Copyright 2015 Dorival Pedroso and Raul Durand. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fem

import (
	"github.com/cpmech/gofem/inp"
	"github.com/cpmech/gofem/shp"

	"github.com/cpmech/gosl/chk"
	"github.com/cpmech/gosl/fun"
	"github.com/cpmech/gosl/la"
	"github.com/cpmech/gosl/tsr"
)

// ElemUP represents an element for porous media based on the u-p formulation [1]
//  References:
//   [1] Pedroso DM (2015) A consistent u-p formulation for porous media with hysteresis.
//       Int Journal for Numerical Methods in Engineering, 101(8) 606-634
//       http://dx.doi.org/10.1002/nme.4808
//   [2] Pedroso DM (2015) A solution to transient seepage in unsaturated porous media.
//       Computer Methods in Applied Mechanics and Engineering, 285 791-816,
//       http://dx.doi.org/10.1016/j.cma.2014.12.009
type ElemUP struct {

	// underlying elements
	U *ElemU // u-element
	P *ElemP // p-element

	// scratchpad. computed @ each ip
	divvs float64     // divvs = div(α4・us - χs) = α4・div(us) - div(χs); (see Eq. 35a [1]) divergence of velocity of solids
	bs    []float64   // bs = as - g = α1・u - ζs - g; (Eqs 35b and A.1 [1]) with 'as' being the acceleration of solids and g, gravity
	hl    []float64   // hl = -ρL・bs - ∇pl; Eq (A.1) of [1]
	Kup   [][]float64 // [nu][np] Kup := dRus/dpl consistent tangent matrix
	Kpu   [][]float64 // [np][nu] Kpu := dRpl/dus consistent tangent matrix
}

// initialisation ///////////////////////////////////////////////////////////////////////////////////

// register element
func init() {

	// information allocator
	infogetters["up"] = func(ndim int, cellType string, faceConds []*FaceCond) *Info {

		// new info
		var info Info

		// p-element cell type
		p_cellType := cellType
		lbb := !Global.Sim.Data.NoLBB
		if lbb {
			p_cellType = shp.GetBasicType(cellType)
		}

		// underlying cells info
		u_info := infogetters["u"](ndim, cellType, faceConds)
		p_info := infogetters["p"](ndim, p_cellType, faceConds)

		// solution variables
		nverts := shp.GetNverts(cellType)
		info.Dofs = make([][]string, nverts)
		for i, dofs := range u_info.Dofs {
			info.Dofs[i] = append(info.Dofs[i], dofs...)
		}
		for i, dofs := range p_info.Dofs {
			info.Dofs[i] = append(info.Dofs[i], dofs...)
		}

		// maps
		info.Y2F = u_info.Y2F
		for key, val := range p_info.Y2F {
			info.Y2F[key] = val
		}

		// t1 and t2 variables
		info.T1vars = p_info.T1vars
		info.T2vars = u_info.T2vars
		return &info
	}

	// element allocator
	eallocators["up"] = func(ndim int, cellType string, faceConds []*FaceCond, cid int, edat *inp.ElemData, x [][]float64) Elem {

		// basic data
		var o ElemUP

		// p-element cell type
		p_cellType := cellType
		lbb := !Global.Sim.Data.NoLBB
		if lbb {
			p_cellType = shp.GetBasicType(cellType)
		}

		// underlying elements
		u_allocator := eallocators["u"]
		p_allocator := eallocators["p"]
		u_elem := u_allocator(ndim, cellType, faceConds, cid, edat, x)
		p_elem := p_allocator(ndim, p_cellType, faceConds, cid, edat, x)
		if LogErrCond(u_elem == nil, "cannot allocate underlying u-element") {
			return nil
		}
		if LogErrCond(p_elem == nil, "cannot allocate underlying p-element") {
			return nil
		}
		o.U = u_elem.(*ElemU)
		o.P = p_elem.(*ElemP)

		// scratchpad. computed @ each ip
		o.bs = make([]float64, ndim)
		o.hl = make([]float64, ndim)
		o.Kup = la.MatAlloc(o.U.Nu, o.P.Np)
		o.Kpu = la.MatAlloc(o.P.Np, o.U.Nu)

		// return new element
		return &o
	}
}

// implementation ///////////////////////////////////////////////////////////////////////////////////

// Id returns the cell Id
func (o ElemUP) Id() int { return o.U.Id() }

// SetEqs set equations
func (o *ElemUP) SetEqs(eqs [][]int, mixedform_eqs []int) (ok bool) {
	ndim := o.U.Ndim
	eqs_u := make([][]int, o.U.Shp.Nverts)
	eqs_p := make([][]int, o.P.Shp.Nverts)
	for m := 0; m < o.U.Shp.Nverts; m++ {
		eqs_u[m] = eqs[m][:ndim]
	}
	idxp := ndim
	for m := 0; m < o.P.Shp.Nverts; m++ {
		eqs_p[m] = []int{eqs[m][idxp]}
	}
	if !o.U.SetEqs(eqs_u, mixedform_eqs) {
		return
	}
	return o.P.SetEqs(eqs_p, nil)
}

// SetEleConds set element conditions
func (o *ElemUP) SetEleConds(key string, f fun.Func, extra string) (ok bool) {
	if !o.U.SetEleConds(key, f, extra) {
		return
	}
	return o.P.SetEleConds(key, f, extra)
}

// InterpStarVars interpolates star variables to integration points
func (o *ElemUP) InterpStarVars(sol *Solution) (ok bool) {
	if !o.U.InterpStarVars(sol) {
		return
	}
	return o.P.InterpStarVars(sol)
}

// adds -R to global residual vector fb
func (o ElemUP) AddToRhs(fb []float64, sol *Solution) (ok bool) {

	// clear variables
	if o.P.DoExtrap {
		la.VecFill(o.P.ρl_ex, 0)
	}

	// for each integration point
	dc := Global.DynCoefs
	ndim := o.U.Ndim
	u_nverts := o.U.Shp.Nverts
	p_nverts := o.P.Shp.Nverts
	var coef, plt, klr, ρL, ρl, ρ, p, Cpl, Cvs, divus, divvs float64
	var err error
	var r int
	for idx, ip := range o.U.IpsElem {

		// interpolation functions, gradients and variables @ ip
		if !o.ipvars(idx, sol) {
			return
		}
		coef = o.U.Shp.J * ip.W
		S := o.U.Shp.S
		G := o.U.Shp.G
		Sb := o.P.Shp.S
		Gp := o.P.Shp.G

		// auxiliary
		σe := o.U.States[idx].Sig
		divus = o.P.States[idx].Divus
		divvs = dc.α4*divus - o.U.divχs[idx] // divergence of Eq. (35a) [1]

		// tpm variables
		plt = dc.β1*o.P.pl - o.P.ψl[idx] // Eq. (35c) [1]
		klr = o.P.Mdl.Cnd.Klr(o.P.States[idx].Sl)
		ρL = o.P.States[idx].RhoL
		ρl, ρ, p, Cpl, Cvs, err = o.P.States[idx].LSvars(o.P.Mdl)
		if LogErr(err, "calc of tpm variables failed") {
			return
		}

		// compute bs, hl and ρwl. see Eqs (34b), (35) and (A.1a) of [1]
		for i := 0; i < ndim; i++ {
			o.bs[i] = dc.α1*o.U.us[i] - o.U.ζs[idx][i] - o.P.g[i]
			o.hl[i] = -ρL*o.bs[i] - o.P.gpl[i]
			o.P.ρwl[i] = 0
			for j := 0; j < ndim; j++ {
				o.P.ρwl[i] += klr * o.P.Mdl.Klsat[i][j] * o.hl[i] // TODO: fix this
			}
		}

		// p: add negative of residual term to fb; see Eqs. (38a) and (45a) of [1]
		for m := 0; m < p_nverts; m++ {
			r = o.P.Pmap[m]
			fb[r] -= coef * Sb[m] * (Cpl*plt + Cvs*divvs)
			for i := 0; i < ndim; i++ {
				fb[r] += coef * Gp[m][i] * o.P.ρwl[i] // += coef * div(ρl*wl)
			}
			if o.P.DoExtrap { // Eq. (19) of [2]
				o.P.ρl_ex[m] += o.P.Emat[m][idx] * ρl
			}
		}

		// u: add negative of residual term to fb; see Eqs. (38b) and (45b) [1]
		for m := 0; m < u_nverts; m++ {
			for i := 0; i < ndim; i++ {
				r = o.U.Umap[i+m*ndim]
				fb[r] -= coef * S[m] * ρ * o.bs[i]
				for j := 0; j < ndim; j++ {
					fb[r] -= coef * tsr.M2T(σe, i, j) * G[m][j]
				}
				fb[r] += coef * p * G[m][i]
			}
		}
	}
	return true
}

// adds element K to global Jacobian matrix Kb
func (o ElemUP) AddToKb(Kb *la.Triplet, sol *Solution, firstIt bool) (ok bool) {

	// clear matrices
	u_nverts := o.U.Shp.Nverts
	p_nverts := o.P.Shp.Nverts
	la.MatFill(o.P.Kpp, 0)
	for i := 0; i < o.U.Nu; i++ {
		for j := 0; j < o.P.Np; j++ {
			o.Kup[i][j] = 0
			o.Kpu[j][i] = 0
		}
		for j := 0; j < o.U.Nu; j++ {
			o.U.K[i][j] = 0
		}
	}
	if o.P.DoExtrap {
		for i := 0; i < p_nverts; i++ {
			o.P.ρl_ex[i] = 0
			for j := 0; j < p_nverts; j++ {
				o.P.dρldpl_ex[i][j] = 0
			}
		}
	}

	// for each integration point
	dc := Global.DynCoefs
	ndim := o.U.Ndim
	var coef, plt, klr, ρL, Cl, divus, divvs float64
	var ρl, ρ, Cpl, Cvs, dρdpl, dpdpl, dCpldpl, dCvsdpl, dklrdpl, dCpldusM, dρdusM float64
	var err error
	var r, c int
	for idx, ip := range o.U.IpsElem {

		// interpolation functions, gradients and variables @ ip
		if !o.ipvars(idx, sol) {
			return
		}
		coef = o.U.Shp.J * ip.W
		S := o.U.Shp.S
		G := o.U.Shp.G
		Sb := o.P.Shp.S
		Gp := o.P.Shp.G

		// auxiliary
		divus = o.P.States[idx].Divus
		divvs = dc.α4*divus - o.U.divχs[idx] // divergence of Eq (35a) [1]

		// tpm variables
		plt = dc.β1*o.P.pl - o.P.ψl[idx] // Eq (35c) [1]
		klr = o.P.Mdl.Cnd.Klr(o.P.States[idx].Sl)
		ρL = o.P.States[idx].RhoL
		Cl = o.P.Mdl.Cl
		ρl, ρ, Cpl, Cvs, dρdpl, dpdpl, dCpldpl, dCvsdpl, dklrdpl, dCpldusM, dρdusM, err = o.P.States[idx].LSderivs(o.P.Mdl)
		if LogErr(err, "calc of tpm derivatives failed") {
			return
		}

		// compute bs and hl. see Eqs (A.1)
		for i := 0; i < ndim; i++ {
			o.bs[i] = dc.α1*o.U.us[i] - o.U.ζs[idx][i] - o.P.g[i]
			o.hl[i] = -ρL*o.bs[i] - o.P.gpl[i]
		}

		// Kpu, Kup and Kpp
		for n := 0; n < p_nverts; n++ {
			for j := 0; j < ndim; j++ {

				// Kpu := ∂Rl^n/∂us^m and Kup := ∂Rus^m/∂pl^n; see Eq (47) of [1]
				for m := 0; m < u_nverts; m++ {
					c = j + m*ndim

					// add ∂rlb/∂us^m: Eqs (A.3) and (A.6) of [1]
					o.Kpu[n][c] += coef * Sb[n] * (dCpldusM*plt + dc.α4*Cvs) * G[m][j]

					// add ∂(ρl.wl)/∂us^m: Eq (A.8) of [1]
					for i := 0; i < ndim; i++ {
						o.Kpu[n][c] += coef * Gp[n][i] * S[m] * dc.α1 * ρL * klr * o.P.Mdl.Klsat[i][j]
					}

					// add ∂rl/∂pl^n and ∂p/∂pl^n: Eqs (A.9) and (A.11) of [1]
					o.Kup[c][n] += coef * (S[m]*Sb[n]*dρdpl*o.bs[j] - G[m][j]*Sb[n]*dpdpl)
				}

				// term in brackets in Eq (A.7) of [1]
				o.P.tmp[j] = Sb[n]*dklrdpl*o.hl[j] - klr*(Sb[n]*Cl*o.bs[j]+Gp[n][j])
			}

			// Kpp := ∂Rl^m/∂pl^n; see Eq (47) of [1]
			for m := 0; m < p_nverts; m++ {

				// add ∂rlb/dpl^n: Eq (A.5) of [1]
				o.P.Kpp[m][n] += coef * Sb[m] * Sb[n] * (dCpldpl*plt + dCvsdpl*divvs + dc.β1*Cpl)

				// add ∂(ρl.wl)/∂us^m: Eq (A.7) of [1]
				for i := 0; i < ndim; i++ {
					for j := 0; j < ndim; j++ {
						o.P.Kpp[m][n] -= coef * Gp[m][i] * o.P.Mdl.Klsat[i][j] * o.P.tmp[j]
					}
				}

				// inner summation term in Eq (22) of [2]
				if o.P.DoExtrap {
					o.P.dρldpl_ex[m][n] += o.P.Emat[m][idx] * Cpl * Sb[n]
				}
			}

			// Eq. (19) of [2]
			if o.P.DoExtrap {
				o.P.ρl_ex[n] += o.P.Emat[n][idx] * ρl
			}
		}

		// Kuu: add ∂rub^m/∂us^n; see Eqs (47) and (A.10) of [1]
		for m := 0; m < u_nverts; m++ {
			for i := 0; i < ndim; i++ {
				r = i + m*ndim
				for n := 0; n < u_nverts; n++ {
					for j := 0; j < ndim; j++ {
						c = j + n*ndim
						o.U.K[r][c] += coef * S[m] * (S[n]*dc.α1*ρ*tsr.It[i][j] + dρdusM*o.bs[i]*G[n][j])
					}
				}
			}
		}

		// Kuu: add stiffness term ∂(σe・G^m)/∂us^n
		if LogErr(o.U.MdlSmall.CalcD(o.U.D, o.U.States[idx], firstIt), "AddToKb") {
			return
		}
		IpAddToKt(o.U.K, u_nverts, ndim, coef, G, o.U.D)
	}
	return true
}

// Update perform (tangent) update
func (o *ElemUP) Update(sol *Solution) (ok bool) {

	// auxiliary
	ndim := o.U.Ndim

	// for each integration point
	var Δpl, divusNew float64
	var r int
	for idx, ip := range o.U.IpsElem {

		// interpolation functions and gradients
		if LogErr(o.P.Shp.CalcAtIp(o.P.X, ip, false), "Update") {
			return
		}
		if LogErr(o.U.Shp.CalcAtIp(o.U.X, ip, true), "Update") {
			return
		}

		// compute Δpl @ ip
		Δpl = 0
		for m := 0; m < o.P.Shp.Nverts; m++ {
			r = o.P.Pmap[m]
			Δpl += o.P.Shp.S[m] * sol.ΔY[r]
		}

		// compute divus @ ip
		divusNew = 0
		for m := 0; m < o.U.Shp.Nverts; m++ {
			for i := 0; i < ndim; i++ {
				r := o.U.Umap[i+m*ndim]
				divusNew += o.U.Shp.G[m][i] * sol.Y[r]
			}
		}

		// p: update internal state
		if LogErr(o.P.Mdl.Update(o.P.States[idx], Δpl, 0, divusNew), "p: update failed") {
			return
		}

		// u: update internal state
		if !o.U.ipupdate(idx, o.U.Shp.S, o.U.Shp.G, sol) {
			return
		}
		//io.Pf("%3d : Δpl=%13.10f pc=%13.10f sl=%13.10f RhoL=%13.10f Wet=%v\n", o.Cell.Id, Δpl, o.States[idx].Pg-o.States[idx].Pl, o.States[idx].Sl, o.States[idx].RhoL, o.States[idx].Wet)
	}
	return true
}

// internal variables ///////////////////////////////////////////////////////////////////////////////

// InitIvs reset (and fix) internal variables after primary variables have been changed
func (o *ElemUP) InitIvs(sol *Solution) (ok bool) {
	if !o.U.InitIvs(sol) {
		return
	}
	return o.P.InitIvs(sol)
}

// SetIvs set secondary variables; e.g. during initialisation via files
func (o *ElemUP) SetIvs(zvars map[string][]float64) (ok bool) {
	if !o.U.SetIvs(zvars) {
		return
	}
	return o.P.SetIvs(zvars)
}

// BackupIvs create copy of internal variables
func (o *ElemUP) BackupIvs() (ok bool) {
	if !o.U.BackupIvs() {
		return
	}
	return o.P.BackupIvs()
}

// RestoreIvs restore internal variables from copies
func (o *ElemUP) RestoreIvs() (ok bool) {
	if !o.U.RestoreIvs() {
		return
	}
	return o.P.RestoreIvs()
}

// writer ///////////////////////////////////////////////////////////////////////////////////////////

// Encode encodes internal variables
func (o ElemUP) Encode(enc Encoder) (ok bool) {
	if !o.U.Encode(enc) {
		return
	}
	return o.P.Encode(enc)
}

// Decode decodes internal variables
func (o ElemUP) Decode(dec Decoder) (ok bool) {
	if !o.U.Decode(dec) {
		return
	}
	return o.P.Decode(dec)
}

// OutIpsData returns data from all integration points for output
func (o ElemUP) OutIpsData() (data []*OutIpData) {
	u_dat := o.U.OutIpsData()
	p_dat := o.P.OutIpsData()
	nip := len(o.U.IpsElem)
	chk.IntAssert(len(u_dat), nip)
	chk.IntAssert(len(u_dat), len(p_dat))
	data = make([]*OutIpData, nip)
	for i, d := range u_dat {
		for key, val := range p_dat[i].V {
			d.V[key] = val
		}
		data[i] = &OutIpData{d.Eid, d.X, d.V}
	}
	return
}

// auxiliary ////////////////////////////////////////////////////////////////////////////////////////

// ipvars computes current values @ integration points. idx == index of integration point
func (o *ElemUP) ipvars(idx int, sol *Solution) (ok bool) {

	// interpolation functions and gradients
	if LogErr(o.P.Shp.CalcAtIp(o.P.X, o.U.IpsElem[idx], true), "ipvars") {
		return
	}
	if LogErr(o.U.Shp.CalcAtIp(o.U.X, o.U.IpsElem[idx], true), "ipvars") {
		return
	}

	// auxiliary
	ndim := o.U.Ndim

	// gravity
	o.P.g[ndim-1] = 0
	if o.P.Gfcn != nil {
		o.P.g[ndim-1] = -o.P.Gfcn.F(sol.T, nil)
	}

	// clear gpl and recover u-variables @ ip
	for i := 0; i < ndim; i++ {
		o.P.gpl[i] = 0 // clear gpl here
		o.U.us[i] = 0
		for m := 0; m < o.U.Shp.Nverts; m++ {
			r := o.U.Umap[i+m*ndim]
			o.U.us[i] += o.U.Shp.S[m] * sol.Y[r]
		}
	}

	// recover p-variables @ ip
	o.P.pl = 0
	for m := 0; m < o.P.Shp.Nverts; m++ {
		r := o.P.Pmap[m]
		o.P.pl += o.P.Shp.S[m] * sol.Y[r]
		for i := 0; i < ndim; i++ {
			o.P.gpl[i] += o.P.Shp.G[m][i] * sol.Y[r]
		}
	}
	return true
}
