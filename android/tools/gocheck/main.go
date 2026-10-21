//go:build gocheck

// gocheck is the Go reference driver for android/tools/hostcheck.cpp: it runs
// the original Go engine (factory.New + TimedEngine.AnalyzeAt) on the same PNG
// frames and prints byte-comparable lines. Excluded from normal builds by the
// gocheck tag. Run from the repo root:
//
//	go run -tags gocheck ./android/tools/gocheck [--seq] [--calibration PROFILE:LB/RB/LA/RA] <png or dir>...
//
// --calibration sets the region calibration (docs/区域校准.md) like hostcheck:
// left beads/right beads/left avatar/right avatar, each "x,y,w,h" (frame
// fractions) or "-" (automatic). The geometry line is the per-frame geometry
// (beads; name/avatar x0,y0,x1,y1; beadBox/avatarBox x,y,w,h fractions).
package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/identity"
)

func load(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	return img, nil
}

func beads(bs []engine.BeadInfo) string {
	var parts []string
	for _, b := range bs {
		st := 'D'
		switch {
		case b.Unknown:
			st = 'U'
		case b.Gold:
			st = 'G'
		case b.Lit:
			st = 'L'
		}
		parts = append(parts, fmt.Sprintf("%s=%c(%.3f)@%d,%d", b.Label, st, b.Conf, b.X, b.Y))
	}
	return strings.Join(parts, " ")
}

// geometry formats Result.Geometry exactly like hostcheck.
func geometry(sides [2]engine.SideGeometry) string {
	rect := func(r image.Rectangle) string { return fmt.Sprintf("%d,%d,%d,%d", r.Min.X, r.Min.Y, r.Max.X, r.Max.Y) }
	box := func(r config.NormalizedRect) string {
		return fmt.Sprintf("%.9f,%.9f,%.9f,%.9f", r.X, r.Y, r.Width, r.Height)
	}
	var parts []string
	for i, g := range sides {
		var beads []string
		for _, p := range g.Beads {
			beads = append(beads, fmt.Sprintf("%d,%d", p.X, p.Y))
		}
		parts = append(parts, fmt.Sprintf("%c beads=%s name=%s avatar=%s beadBox=%s avatarBox=%s", "LR"[i], strings.Join(beads, ";"),
			rect(g.NameROI), rect(g.AvatarROI), box(g.BeadBox), box(g.AvatarBox)))
	}
	return strings.Join(parts, " | ")
}

// parseCalibration reads "PROFILE:LB/RB/LA/RA" (hostcheck uses the same form).
func parseCalibration(arg string) (*config.RegionCalibration, error) {
	profile, boxes, ok := strings.Cut(arg, ":")
	parts := strings.Split(boxes, "/")
	if !ok || len(parts) != 4 {
		return nil, fmt.Errorf("bad --calibration %q", arg)
	}
	c := &config.RegionCalibration{Profile: profile}
	for i, dst := range []**config.NormalizedRect{&c.LeftBeads, &c.RightBeads, &c.LeftAvatar, &c.RightAvatar} {
		if parts[i] == "-" {
			continue
		}
		fields := strings.Split(parts[i], ",")
		if len(fields) != 4 {
			return nil, fmt.Errorf("bad --calibration box %q", parts[i])
		}
		var v [4]float64
		for k, f := range fields {
			var err error
			if v[k], err = strconv.ParseFloat(f, 64); err != nil {
				return nil, err
			}
		}
		*dst = &config.NormalizedRect{X: v[0], Y: v[1], Width: v[2], Height: v[3]}
	}
	return c, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func main() {
	var files []string
	seq := false
	var calibration *config.RegionCalibration
	for i := 1; i < len(os.Args); i++ {
		a := os.Args[i]
		if a == "--seq" {
			seq = true
			continue
		}
		if a == "--calibration" && i+1 < len(os.Args) {
			i++
			var err error
			if calibration, err = parseCalibration(os.Args[i]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			continue
		}
		st, err := os.Stat(a)
		if err == nil && st.IsDir() {
			ents, _ := os.ReadDir(a)
			var names []string
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".png") {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, n := range names {
				files = append(files, filepath.Join(a, n))
			}
		} else {
			files = append(files, a)
		}
	}
	cfg := config.Default()
	cfg.Layout.Calibration = calibration
	identity.SetMineNames(cfg.UI.PlayerNames)
	base := time.Unix(1700000000, 0)
	var shared engine.Engine
	for i, f := range files {
		img, err := load(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "skip", f, err)
			continue
		}
		eng := shared
		at := base
		if seq {
			// --seq: one engine, frames 100ms apart (hostcheck --seq uses frame::Pipeline).
			at = base.Add(time.Duration(i) * 100 * time.Millisecond)
		}
		if eng == nil {
			eng, err = factory.New(factory.FromApp(cfg))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if seq {
				shared = eng
			}
		}
		r := eng.(engine.TimedEngine).AnalyzeAt(img, at)
		fmt.Printf("%s\tfight=%d unc=%d scene=%s gate=%.4f profile=%s open=%d slots=%d/%d L=%s R=%s side=%s opp=%s name=%s\n\tbeads: %s\n\tgeometry: %s\n",
			filepath.Base(f), b2i(r.Fighting), b2i(r.Uncertain), r.Scene, r.GateScore, r.LayoutProfile, b2i(r.RoundOpening),
			r.LeftSlots, r.RightSlots, r.LeftNinja, r.RightNinja, r.PlayerSide, r.OppName, r.Name, beads(r.Beads), geometry(r.Geometry))
	}
}
