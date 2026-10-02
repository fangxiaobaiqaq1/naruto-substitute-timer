package ninja

// Verbatim copies of the pre-parallel implementations (serial NCC under the
// catalog lock, full-frame portrait gray, At()-based gray/mask, serial
// catalog load). The equivalence tests compare the current code against them.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"

	"narutotimer/internal/match"
)

func legacyMatch(c *avatarCatalog, img *image.RGBA, roi image.Rectangle, scale float64) AvatarMatch {
	if c == nil || img == nil || scale <= 0 {
		return AvatarMatch{}
	}
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return AvatarMatch{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	view := img.SubImage(roi).(*image.RGBA)
	gray := match.ToGray(view)
	coarse := match.ScaleGray(gray, 36, 36)
	// The portrait is a fixed HUD element: relative to the canonical
	// AvatarRegion window its diamond center sits near (35.5, 48) reference
	// pixels and renders at roughly 0.6x the 85px catalog asset. A square crop
	// around that anchor removes the ROI's background and vertical squash from
	// the 36x36 locator. The unanchored crop stays as a fallback for callers
	// that pass a synthetic centered portrait rather than the HUD ROI.
	// gray has origin (0,0) at roi.Min; cropping it equals converting the
	// cropped RGBA because luma is per pixel.
	anchor := avatarAnchorRect(roi, scale).Intersect(roi).Sub(roi.Min)
	hudCoarse := match.ScaleGray(gray.SubImage(anchor).(*image.Gray), 36, 36)
	best := []avatarFinalist(nil)
	for _, entry := range c.entries {
		// Two aligned 36² thumbnail queries (HUD anchor + whole ROI) keep
		// recall for both real HUD frames and centered synthetic portraits.
		// Only the retained candidates do full-resolution NCC.
		score := coarseAvatarScore(coarse, entry.coarseGray, entry.coarseMask)
		if hud := coarseAvatarScore(hudCoarse, entry.coarseGray, entry.coarseMask); hud > score {
			score = hud
		}
		best = append(best, avatarFinalist{entry, score})
	}
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].score != best[j].score {
			return best[i].score > best[j].score
		}
		return best[i].entry.id < best[j].entry.id
	})
	// Coarse matching is only a locator. A real HUD portrait can be partially
	// clipped by the capture boundary and its render scale is not guaranteed to
	// equal the bead-derived scale, so the true entry may rank below the first
	// few color-similar portraits (measured worst case: 水门 at rank 10). Keep
	// this bounded recall set; strict multi-scale NCC and the score margin
	// still decide identity.
	if len(best) > 24 {
		best = best[:24]
	}
	entries := []*avatarEntry(nil)
	for i := range best {
		entries = append(entries, best[i].entry)
	}
	return legacyMatchEntries(c, view, gray, scale, entries)
}

// legacyMatchEntries: caller holds c.mu.
func legacyMatchEntries(c *avatarCatalog, view *image.RGBA, gray *image.Gray, scale float64, entries []*avatarEntry) AvatarMatch {
	var out AvatarMatch
	if gray == nil {
		gray = match.ToGray(view)
	}
	// Keep the best score per identity before calculating the runner-up. A
	// single identity is evaluated at several display scales; treating its
	// second scale as a different candidate can reject an otherwise unambiguous
	// portrait (best=.90, same-ID runner-up=.85).
	bestByID, nameByID, baseByID := map[string]float64{}, map[string]string{}, map[string]string{}
	bounds := view.Bounds()
	c.evictTemplates(scale)
	decode := func(entry *avatarEntry) bool {
		if entry == nil {
			return false
		}
		if entry.gray == nil {
			img, _, err := image.Decode(bytes.NewReader(entry.data))
			if err != nil {
				return false
			}
			entry.gray, entry.mask = avatarGrayMask(img)
			entry.mask = avatarFaceMask(entry.gray.Bounds(), entry.mask)
		}
		return true
	}
	scoreAt := func(entry *avatarEntry, factor float64) float64 {
		w, h := avatarFactorSize(entry, scale, factor)
		if w > bounds.Dx() || h > bounds.Dy() {
			// NCC cannot slide a template larger than the ROI; skip the wasted
			// prepare instead of measuring a meaningless zero score.
			return 0
		}
		score, err := (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: view.Bounds(), Prepared: c.prepared(entry, w, h)})
		if err != nil {
			return 0
		}
		return score.Value
	}
	for _, entry := range entries {
		if !decode(entry) {
			continue
		}
		out.ids = append(out.ids, entry.id)
		nameByID[entry.id] = entry.name
		baseByID[entry.id] = entry.baseName
	}
	if len(entries) > 6 {
		// Full scan, two-stage: the primary display factor (the live HUD
		// renders near 0.6x bead scale) locates the portrait cheaply over the
		// whole shortlist; the full multi-scale sweep below then re-runs only
		// the leading candidates plus the coarse locator's first entries. The
		// shortlist order itself is the recall fallback for render scales that
		// are not the common HUD factor (exact-scale test callers).
		for _, entry := range entries {
			if value := scoreAt(entry, .60); value > bestByID[entry.id] {
				bestByID[entry.id] = value
			}
		}
		primary := []stageEntry(nil)
		for _, entry := range entries {
			if entry != nil {
				primary = append(primary, stageEntry{entry, bestByID[entry.id]})
			}
		}
		sort.SliceStable(primary, func(i, j int) bool {
			if primary[i].score != primary[j].score {
				return primary[i].score > primary[j].score
			}
			return primary[i].entry.id < primary[j].entry.id
		})
		fine := map[string]bool{}
		for i, v := range primary {
			if i < 6 {
				fine[v.entry.id] = true
			}
		}
		for i, entry := range entries {
			if entry != nil && i < 6 {
				fine[entry.id] = true
			}
		}
		for _, entry := range entries {
			if entry != nil && fine[entry.id] {
				for _, factor := range avatarFactorsFull {
					if value := scoreAt(entry, factor); value > bestByID[entry.id] {
						bestByID[entry.id] = value
					}
				}
			}
		}
	} else {
		for _, entry := range entries {
			for _, factor := range avatarFactorsFull {
				if value := scoreAt(entry, factor); value > bestByID[entry.id] {
					bestByID[entry.id] = value
				}
			}
		}
	}
	var runnerName string
	for id, score := range bestByID {
		if score > out.Score {
			out.RunnerUp, runnerName = out.Score, out.Name
			out.ID, out.Name, out.BaseName, out.Score = id, nameByID[id], baseByID[id], score
		} else if id != out.ID && score > out.RunnerUp {
			out.RunnerUp, runnerName = score, nameByID[id]
		}
	}
	out.Candidate = out.Name
	if out.Score < .78 || out.Score-out.RunnerUp < .08 {
		// A lookalike skin pair of the same cosmetic family (both golden Naruto
		// forms under a glare) can compress the margin below the strict gate.
		// Accept a very strong winner only when NEITHER candidate carries a
		// special variant policy: geometry rules (slots/palette/row offset)
		// stay strictly margined, so special-bead safety is unchanged.
		if !(out.Score >= .90 && out.Score-out.RunnerUp >= .02 && !avatarVariantPolicy(out.Name) && !avatarVariantPolicy(runnerName)) {
			out.ID, out.Name, out.BaseName = "", "", ""
		}
	}
	return out
}

func legacyLoadAvatarCatalog(data []byte, readFile func(string) ([]byte, error)) (*avatarCatalog, error) {
	var index avatarIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse avatar index: %w", err)
	}
	if len(index.Entries) == 0 {
		return nil, fmt.Errorf("avatar index has no entries")
	}
	catalog := &avatarCatalog{entries: make([]*avatarEntry, 0, len(index.Entries)), byID: make(map[string]*avatarEntry, len(index.Entries))}
	for _, item := range index.Entries {
		sum := item.SHA256
		if sum == "" {
			sum = item.Avatar.SHA256
		}
		if !validAvatarID(item.ID) || catalog.byID[item.ID] != nil || len(sum) != 64 {
			return nil, fmt.Errorf("invalid or duplicate avatar id %q", item.ID)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("invalid avatar sha256 for %s", item.ID)
		}
		png, err := readFile(item.ID)
		if err != nil {
			return nil, fmt.Errorf("read avatar %s: %w", item.ID, err)
		}
		got := sha256.Sum256(png)
		if !strings.EqualFold(hex.EncodeToString(got[:]), sum) {
			return nil, fmt.Errorf("avatar %s sha256 does not match index", item.ID)
		}
		img, _, err := image.Decode(bytes.NewReader(png))
		if err != nil {
			return nil, fmt.Errorf("decode avatar %s: %w", item.ID, err)
		}
		gray, alphaMask := legacyAvatarGrayMask(img)
		if gray.Bounds().Dx() < 16 || gray.Bounds().Dy() < 16 {
			return nil, fmt.Errorf("avatar %s too small", item.ID)
		}
		// The catalog PNG carries a baked rank badge/frame/background. Those
		// pixels vary in the live HUD, while the face inside the diamond is
		// stable. Recognition uses alpha ∩ inner-diamond only.
		mask := avatarFaceMask(gray.Bounds(), alphaMask)
		thumbGray, thumbMask := match.ScaleGray(gray, 16, 16), match.ScaleGray(mask, 16, 16)
		entry := &avatarEntry{id: item.ID, name: avatarName(item), baseName: avatarBaseName(item), data: png, thumb: match.PrepareNCC(thumbGray, thumbMask), thumbGray: thumbGray, thumbMask: thumbMask, coarseGray: match.ScaleGray(gray, 36, 36), coarseMask: match.ScaleGray(mask, 36, 36)}
		catalog.entries, catalog.byID[item.ID] = append(catalog.entries, entry), entry
	}
	return catalog, nil
}

func legacyAvatarGrayMask(src image.Image) (*image.Gray, *image.Gray) {
	b := src.Bounds()
	gray, mask := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy())), image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			gray.SetGray(x, y, color.Gray{Y: uint8((299*(r>>8) + 587*(g>>8) + 114*(bl>>8) + 500) / 1000)})
			if a >= 0x8000 {
				mask.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	return gray, mask
}

func legacyRead(r *Reader, img *image.RGBA, roi image.Rectangle, scale float64) evidence {
	if r == nil || img == nil || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return evidence{}
	}
	r.mu.Lock()
	if r.prepared == nil || r.scale != scale {
		r.prepared = nil
		r.scale = scale
		for _, t := range r.source {
			w, h := int(math.Round(float64(t.gray.Bounds().Dx())*scale)), int(math.Round(float64(t.gray.Bounds().Dy())*scale))
			if w < 8 || h < 8 {
				continue
			}
			gray := match.ScaleGray(t.gray, w, h)
			coarseScale := min(scale, .5)
			small := match.ScaleGray(t.gray, int(math.Round(float64(t.gray.Bounds().Dx())*coarseScale)), int(math.Round(float64(t.gray.Bounds().Dy())*coarseScale)))
			prepared := scaledName{readout: t.readout, ncc: match.PrepareNCC(gray, nil), size: gray.Bounds().Size(), coarse: match.PrepareNCC(small, nil), requirePortrait: t.requirePortrait}
			if t.portrait != nil {
				pw := int(math.Round(float64(t.portrait.Bounds().Dx()) * scale))
				ph := int(math.Round(float64(t.portrait.Bounds().Dy()) * scale))
				portrait := match.ScaleGray(t.portrait, pw, ph)
				prepared.portrait = match.PrepareNCC(portrait, nil)
				prepared.portraitSize = portrait.Bounds().Size()
			}
			r.prepared = append(r.prepared, prepared)
		}
	}
	prepared := r.prepared
	r.mu.Unlock()
	// 百战鼬 requires two independent, current-frame signals: its complete title
	// and its fixed right-HUD portrait. The portrait alone cannot move the bean
	// row or turn a base Itachi into the 百战 skin.
	roi = roi.Intersect(img.Bounds())
	if roi.Empty() {
		return evidence{}
	}
	view := img.SubImage(roi).(*image.RGBA)
	gray := match.ToGray(view)
	// Unknown names used to scan the whole corner at native resolution. Keep
	// the locator bounded at the 480-wide HUD scale, then validate candidate
	// locations using ALL original-resolution pixels and the original threshold.
	factor := min(scale, .5) / scale
	smallGray := match.ScaleGray(gray, max(1, int(math.Round(float64(gray.Bounds().Dx())*factor))), max(1, int(math.Round(float64(gray.Bounds().Dy())*factor))))
	smallImage := image.NewRGBA(smallGray.Bounds())
	var best evidence
	scores := map[string]float64{}
	for _, t := range prepared {
		coarse, err := (match.NCC{}).Match(match.Query{Image: smallImage, Gray: smallGray, ROI: smallImage.Bounds(), Prepared: t.coarse})
		if err != nil || coarse.Value < .55 {
			continue
		}
		point := image.Pt(roi.Min.X+int(math.Round(float64(coarse.Peak.X)/factor)), roi.Min.Y+int(math.Round(float64(coarse.Peak.Y)/factor)))
		radius := int(math.Ceil(2 / factor))
		candidate := image.Rectangle{Min: point, Max: point.Add(t.size)}.Inset(-radius).Intersect(roi)
		score, err := (match.NCC{}).Match(match.Query{Image: view, Gray: gray, ROI: candidate, Prepared: t.ncc})
		if err != nil {
			continue
		}
		scores[t.readout.Name] = max(scores[t.readout.Name], score.Value)
		if score.Value > best.Score {
			best = evidence{Readout: t.readout, template: t.ncc, rect: image.Rectangle{Min: score.Peak, Max: score.Peak.Add(t.size)}}
			best.Score = score.Value
		}
	}
	runnerUp := 0.0
	for name, score := range scores {
		if name != best.Name {
			runnerUp = max(runnerUp, score)
		}
	}
	if best.Score < 0.80 || best.Score-runnerUp < 0.08 {
		return evidence{}
	}
	// 百战鼬 requires two independent, current-frame signals: its complete title
	// and its fixed right-HUD portrait. Keep the verified portrait template on
	// the evidence so the Tracker's fast path can re-check it on later frames.
	if best.Name == ItachiHyakusen {
		t := scaledNameForEvidence(prepared, best)
		if !legacyItachiPortraitEvidence(img, scale, t.portrait, t.portraitSize) {
			return evidence{}
		}
		best.portrait, best.portraitSize = t.portrait, t.portraitSize
	}
	return best
}

func legacyItachiPortraitEvidence(img *image.RGBA, scale float64, portrait *match.PreparedNCC, size image.Point) bool {
	if portrait == nil || size.X <= 0 || size.Y <= 0 {
		return false
	}
	bounds := img.Bounds()
	// The supplied portrait is a right-HUD visual anchor. Do not mirror it to
	// the left: insufficient evidence must remain unknown, never a guessed
	// special offset. Coordinates are normalized to the 960-wide HUD reference.
	x0, x1, y0 := 847.5, 918.75, 7.5
	rect := image.Rect(bounds.Min.X+int(math.Round(x0*scale)), bounds.Min.Y+int(math.Round(y0*scale)), bounds.Min.X+int(math.Round(x1*scale)), bounds.Min.Y+int(math.Round((y0+71)*scale))).Intersect(bounds)
	if rect.Size() != size {
		return false
	}
	gray := match.ToGray(img)
	score, err := (match.NCC{}).Match(match.Query{Image: img, Gray: gray, ROI: rect, Prepared: portrait})
	return err == nil && score.Value >= .78
}
