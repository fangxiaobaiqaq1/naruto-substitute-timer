package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"narutotimer/internal/config"
	"narutotimer/internal/engine/factory"
	"narutotimer/internal/hudtext"
	"narutotimer/internal/ocr"
)

// ocrInspect is opt-in, offline diagnostics. Its output includes visible account
// names, so no raw reports are written during ordinary application capture.
func ocrInspect(args []string) error {
	flags := flag.NewFlagSet("ocr", flag.ContinueOnError)
	cfgPath := flags.String("config", "config.json", "application configuration")
	file := flags.String("image", "", "full game screenshot, required")
	profile := flags.String("profile", "auto", "auto, camp or duel")
	output := flags.String("out", "", "optional NEW JSON file (contains visible names)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" || (*profile != "auto" && *profile != "camp" && *profile != "duel") {
		return fmt.Errorf("image required; profile must be auto, camp or duel")
	}
	if *output != "" {
		if _, err := os.Stat(*output); !os.IsNotExist(err) {
			return fmt.Errorf("output file must not exist: %s", *output)
		}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	img, err := loadRGBA(*file)
	if err != nil {
		return err
	}
	if *profile == "auto" {
		detector, err := factory.New(factory.FromApp(cfg))
		if err != nil {
			return err
		}
		res := detector.Analyze(img)
		if !res.Fighting || res.LayoutProfile == "" {
			return fmt.Errorf("battle HUD not established; explicit calibrated profile required for inspection")
		}
		*profile = res.LayoutProfile
	}
	reader := ocr.NewLocal()
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	report, err := hudtext.Diagnose(ctx, reader, img, cfg, *profile)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{"image": *file, "profile": *profile, "elapsedMs": time.Since(started).Seconds() * 1000, "report": report, "note": "Local OCR with embedded model. Three visual treatments; exact consensus is not a measured accuracy guarantee. Live mode also requires two results and unchanged lettering pixels."}, "", "  ")
	if err != nil {
		return err
	}
	if *output != "" {
		f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(append(data, '\n'))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	fmt.Println(string(data))
	return nil
}
