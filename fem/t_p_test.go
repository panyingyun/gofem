// Copyright 2015 Dorival Pedroso and Raul Durand. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fem

import (
	"sort"
	"testing"

	"github.com/cpmech/gosl/chk"
	"github.com/cpmech/gosl/io"
	"github.com/cpmech/gosl/utl"
)

func Test_p01a(tst *testing.T) {

	/* this tests simulates seepage flow along a column
	 * by reducing the initial hydrostatic pressure at
	 * at the bottom of the column
	 *
	 *      Nodes / Tags                 Equations
	 *
	 *     8 o----o----o 9 (-5)      22 o----o----o 21
	 *       |   14    |                |   24    |
	 *       |         |                |         |
	 *    21 o    o    o 22 (-6)     25 o    o    o 23
	 *       |   26    |                |   26    |
	 *       |         |                |         |
	 *     6 o----o----o 7 (-4)      16 o----o----o 15
	 *       |   13    |                |   18    |
	 *       |         |                |         |
	 *    19 |    o    o 20 (-6)     19 |    o    o 17
	 *       |   25    |                |   20    |
	 *       |         |                |         |
	 *     4 o----o----o 5 (-3)      10 o----o----o 9
	 *       |   12    |                |   12    |
	 *       |         |                |         |
	 *    17 o    o    o 18 (-6)     13 o    o    o 11
	 *       |   24    |                |   14    |
	 *       |         |                |         |
	 *     2 o----o----o 3 (-2)       3 o----o----o 2
	 *       |   11    |                |    6    |
	 *       |         |                |         |
	 *    15 o    o    o 16 (-6)      7 o    o    o 5
	 *       |   23    |                |    8    |
	 *       |         |                |         |
	 *     0 o----o----o 1 (-1)       0 o----o----o 1
	 *           10                          4
	 */

	//verbose()
	chk.PrintTitle("p01a")

	// start simulation
	if !Start("data/p01.sim", true, chk.Verbose) {
		tst.Errorf("test failed\n")
		return
	}

	// make sure to flush log
	defer End()

	// domain
	distr := false
	dom := NewDomain(Global.Sim.Regions[0], distr)
	if dom == nil {
		tst.Errorf("test failed\n")
		return
	}

	// set stage
	if !dom.SetStage(0, Global.Sim.Stages[0], distr) {
		tst.Errorf("test failed\n")
		return
	}

	// nodes and elements
	chk.IntAssert(len(dom.Nodes), 27)
	chk.IntAssert(len(dom.Elems), 4)

	// check dofs
	for _, nod := range dom.Nodes {
		chk.IntAssert(len(nod.Dofs), 1)
		chk.StrAssert(nod.Dofs[0].Key, "pl")
	}

	// check equations
	nids, eqs := get_nids_eqs(dom)
	chk.Ints(tst, "nids", nids, []int{
		0, 1, 3, 2, 10, 16, 11, 15, 23,
		5, 4, 18, 12, 17, 24,
		7, 6, 20, 13, 19, 25,
		9, 8, 22, 14, 21, 26,
	})
	chk.Ints(tst, "eqs", eqs, utl.IntRange(27))

	// check pmap
	pmaps := [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8},
		{3, 2, 9, 10, 6, 11, 12, 13, 14},
		{10, 9, 15, 16, 12, 17, 18, 19, 20},
		{16, 15, 21, 22, 18, 23, 24, 25, 26},
	}
	for i, ele := range dom.Elems {
		e := ele.(*ElemP)
		io.Pforan("e%d.pmap = %v\n", e.Id(), e.Pmap)
		chk.Ints(tst, "pmap", e.Pmap, pmaps[i])
	}

	// constraints
	chk.IntAssert(len(dom.EssenBcs.Bcs), 3)
	var ct_pl_eqs []int // equations with pl prescribed [sorted]
	for _, c := range dom.EssenBcs.Bcs {
		chk.IntAssert(len(c.Eqs), 1)
		eq := c.Eqs[0]
		io.Pforan("key=%v eq=%v\n", c.Key, eq)
		switch c.Key {
		case "pl":
			ct_pl_eqs = append(ct_pl_eqs, eq)
		default:
			tst.Errorf("key %s is incorrect", c.Key)
		}
	}
	sort.Ints(ct_pl_eqs)
	chk.Ints(tst, "equations with pl prescribed", ct_pl_eqs, []int{0, 1, 4})

	// initial values @ nodes
	io.Pforan("initial values @ nodes\n")
	for _, nod := range dom.Nodes {
		z := nod.Vert.C[1]
		eq := nod.Dofs[0].Eq
		pl := dom.Sol.Y[eq]
		plC, _, _ := Global.HydroSt.Calc(z)
		chk.Scalar(tst, io.Sf("nod %3d : pl(@ %4g)= %6g", nod.Vert.Id, z, pl), 1e-17, pl, plC)
	}

	// intial values @ integration points
	io.Pforan("initial values @ integration points\n")
	for _, ele := range dom.Elems {
		e := ele.(*ElemP)
		for idx, ip := range e.IpsElem {
			s := e.States[idx]
			z := e.Shp.IpRealCoords(e.X, ip)[1]
			plC, ρlC, _ := Global.HydroSt.Calc(z)
			chk.Scalar(tst, io.Sf("sl(@ %18g)= %18g", z, s.Sl), 1e-17, s.Sl, 1)
			chk.Scalar(tst, io.Sf("pl(@ %18g)= %18g", z, s.Pl), 1e-10, s.Pl, plC)
			chk.Scalar(tst, io.Sf("ρL(@ %18g)= %18g", z, s.RhoL), 1e-13, s.RhoL, ρlC)
		}
	}
}

func Test_p01b(tst *testing.T) {

	chk.PrintTitle("p01b")

	// run simulation
	if !Start("data/p01.sim", true, chk.Verbose) {
		tst.Errorf("test failed\n")
		return
	}

	// make sure to flush log
	defer End()

	// run simulation
	if !Run() {
		tst.Errorf("test failed\n")
		return
	}
}

func Test_p02_(tst *testing.T) {

	//verbose()
	chk.PrintTitle("p02_")

	// run simulation
	if !Start("data/p02.sim", true, chk.Verbose) {
		tst.Errorf("test failed\n")
		return
	}

	// make sure to flush log
	defer End()

	// for debugging Kb
	if true {
		defer p_DebugKb(&testKb{
			tst: tst, eid: 3, tol: 1e-4, verb: chk.Verbose,
			ni: 1, nj: 1, itmin: 0, itmax: -1, tmin: 5000, tmax: 5000,
		})()
	}

	// run simulation
	if !Run() {
		tst.Errorf("test failed\n")
		return
	}
}
