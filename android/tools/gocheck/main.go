//go:build gocheck

// gocheck is the Go reference driver for android/tools/hostcheck.cpp: it runs
// the original Go engine (factory.New + TimedEngine.AnalyzeAt) on the same PNG
// frames and prints byte-comparable lines. Excluded from normal builds by the
// gocheck tag. Run from the repo root:
//
//	go run -tags gocheck ./android/tools/gocheck <png or dir>...
package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
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

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func main() {
	var files []string
	seq := false
	for _, a := range os.Args[1:] {
		if a == "--seq" {
			seq = true
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
		fmt.Printf("%s\tfight=%d unc=%d scene=%s gate=%.4f profile=%s open=%d slots=%d/%d L=%s R=%s side=%s opp=%s name=%s\n\tbeads: %s\n",
			filepath.Base(f), b2i(r.Fighting), b2i(r.Uncertain), r.Scene, r.GateScore, r.LayoutProfile, b2i(r.RoundOpening),
			r.LeftSlots, r.RightSlots, r.LeftNinja, r.RightNinja, r.PlayerSide, r.OppName, r.Name, beads(r.Beads))
	}
}
