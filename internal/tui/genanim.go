package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// genWave renders the "generating" animation: a calm wave marching across a
// fixed-width track of vertical block glyphs. It is intentionally smooth and
// low-contrast (no flashing) so it reads as "actively working" without being
// distracting — friendly to reduced-motion sensibilities.
//
// The wave is deterministic in (frame, width), so a given frame always renders
// the same string — which makes it easy to test and lets the caller advance it
// one frame per tick.

// genWaveWidth is the number of glyph columns in the animation track.
const genWaveWidth = 14

// blockRamp is the 8-level vertical block ramp (low → high). Index 0 is the
// shortest visible block; higher indices are taller.
var blockRamp = []rune("▁▂▃▄▅▆▇█")

// genWaveLevels returns the per-column ramp index (0..len(blockRamp)-1) for the
// given frame. A travelling sine wave gives a smooth left-to-right motion. Pure
// + deterministic so it can be unit-tested without rendering.
func genWaveLevels(frame, width int) []int {
	if width < 1 {
		width = 1
	}
	levels := make([]int, width)
	top := len(blockRamp) - 1
	for x := 0; x < width; x++ {
		// Two summed sines at different speeds/wavelengths give an organic,
		// non-repetitive-looking ripple while staying calm.
		phase := float64(x)*0.55 - float64(frame)*0.35
		v := math.Sin(phase) + 0.5*math.Sin(phase*0.5+1.7)
		// Map [-1.5, 1.5] → [0, top].
		n := int(math.Round((v + 1.5) / 3.0 * float64(top)))
		if n < 0 {
			n = 0
		}
		if n > top {
			n = top
		}
		levels[x] = n
	}
	return levels
}

// genWaveView renders the wave for the given frame as a styled string. The crest
// (taller columns) uses the brighter indigo; the trough uses the dimmer accent —
// a gentle two-tone gradient rather than a hard flash.
func genWaveView(frame int) string {
	levels := genWaveLevels(frame, genWaveWidth)
	top := len(blockRamp) - 1
	var b strings.Builder
	for _, lvl := range levels {
		ch := string(blockRamp[lvl])
		// Crest columns (upper third of the ramp) get the brighter shade.
		if lvl >= top-2 {
			b.WriteString(lipgloss.NewStyle().Foreground(colAccent2).Render(ch))
		} else {
			b.WriteString(styAcc.Render(ch))
		}
	}
	return b.String()
}
