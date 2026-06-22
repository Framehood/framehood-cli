package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// --- pure animation logic ---

func TestGenWaveLevels_ShapeAndBounds(t *testing.T) {
	top := len(blockRamp) - 1
	for _, frame := range []int{0, 1, 5, 13, 99} {
		levels := genWaveLevels(frame, genWaveWidth)
		if len(levels) != genWaveWidth {
			t.Fatalf("frame %d: len = %d, want %d", frame, len(levels), genWaveWidth)
		}
		for x, lvl := range levels {
			if lvl < 0 || lvl > top {
				t.Errorf("frame %d col %d: level %d out of [0,%d]", frame, x, lvl, top)
			}
		}
	}
}

func TestGenWaveLevels_Deterministic(t *testing.T) {
	a := genWaveLevels(7, genWaveWidth)
	b := genWaveLevels(7, genWaveWidth)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same frame must be deterministic: a=%v b=%v", a, b)
		}
	}
}

func TestGenWaveLevels_Advances(t *testing.T) {
	// Consecutive frames must differ (the wave is actually moving).
	prev := genWaveLevels(0, genWaveWidth)
	moved := false
	for f := 1; f < 6; f++ {
		cur := genWaveLevels(f, genWaveWidth)
		if !equalInts(prev, cur) {
			moved = true
			break
		}
		prev = cur
	}
	if !moved {
		t.Error("the wave must change between consecutive frames")
	}
}

func TestGenWaveLevels_MinWidthClamp(t *testing.T) {
	// width < 1 must not panic and clamps to a single column.
	if got := genWaveLevels(3, 0); len(got) != 1 {
		t.Errorf("width 0 → len %d, want 1 (clamped)", len(got))
	}
}

func TestGenWaveView_RendersWidthCols(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii) // strip color → count plain glyphs
	out := genWaveView(0)
	// Every cell renders exactly one block glyph; with color stripped the visible
	// width equals genWaveWidth.
	if w := lipgloss.Width(out); w != genWaveWidth {
		t.Errorf("rendered wave width = %d, want %d (\"%s\")", w, genWaveWidth, out)
	}
	// All glyphs must come from the block ramp.
	for _, r := range stripANSI(out) {
		if !strings.ContainsRune(string(blockRamp), r) {
			t.Errorf("unexpected glyph %q in wave render", r)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stripANSI removes SGR escape sequences so we can inspect the raw glyphs. With
// the Ascii color profile there are none, but this keeps the test robust.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- model sub-state: submitting vs generating ---

func TestGenerating_Discriminator(t *testing.T) {
	m := newTestModel()

	// idle → not generating.
	if m.generating() {
		t.Error("idle model must not report generating")
	}

	// submitting: working but no job id yet.
	m.phase = phaseWorking
	m.jobID = ""
	if m.generating() {
		t.Error("submitting (no job id) must not report generating")
	}

	// generating: working with a job id.
	m.jobID = "job_x"
	if !m.generating() {
		t.Error("working + job id must report generating")
	}

	// done with a leftover id → not working, not generating.
	m.phase = phaseDone
	if m.generating() {
		t.Error("done phase must not report generating")
	}
}

func TestStatusView_SubmittingState(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newTestModel()
	m.phase = phaseWorking
	m.jobID = "" // pre-job
	out := m.workingView()
	if !strings.Contains(out, "submitting") {
		t.Errorf("pre-job working view should show 'submitting': %q", out)
	}
	if strings.Contains(out, "generating") {
		t.Errorf("submitting view must not claim 'generating': %q", out)
	}
	// No wave glyphs in the submitting state.
	for _, r := range blockRamp {
		if strings.ContainsRune(out, r) {
			t.Errorf("submitting view should not render the wave glyph %q", string(r))
		}
	}
}

func TestStatusView_GeneratingState(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newTestModel()
	m.phase = phaseWorking
	m.jobID = "job_abc123"
	m.status = "running"
	out := m.workingView()
	if !strings.Contains(out, "generating") {
		t.Errorf("job-in-flight view should show 'generating': %q", out)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("generating view should show the provider status: %q", out)
	}
	if !strings.Contains(out, "job_abc123") {
		t.Errorf("generating view should show the job id: %q", out)
	}
	// The wave must be present (at least one ramp glyph).
	hasWave := false
	for _, r := range blockRamp {
		if strings.ContainsRune(out, r) {
			hasWave = true
			break
		}
	}
	if !hasWave {
		t.Errorf("generating view should render the wave animation: %q", out)
	}
}

// TestGenFrameAdvancesOnTick verifies the animation frame advances on the
// spinner tick while generating, and stays put while merely submitting.
func TestGenFrameAdvancesOnTick(t *testing.T) {
	// Generating: tick advances the frame.
	m := newTestModel()
	m.phase = phaseWorking
	m.jobID = "job_x"
	start := m.genFrame
	nm, _ := m.Update(spinner.TickMsg{})
	got := nm.(model)
	if got.genFrame != start+1 {
		t.Errorf("generating: genFrame = %d after one tick, want %d", got.genFrame, start+1)
	}
	// A second tick advances again.
	nm2, _ := got.Update(spinner.TickMsg{})
	if nm2.(model).genFrame != start+2 {
		t.Errorf("generating: genFrame = %d after two ticks, want %d", nm2.(model).genFrame, start+2)
	}

	// Submitting (no job id): the wave frame does NOT advance.
	s := newTestModel()
	s.phase = phaseWorking
	s.jobID = ""
	s0 := s.genFrame
	ns, _ := s.Update(spinner.TickMsg{})
	if ns.(model).genFrame != s0 {
		t.Errorf("submitting: genFrame should not advance, got %d want %d", ns.(model).genFrame, s0)
	}
}

// TestGeneratingRendersDistinctFrames sanity-checks that two different frames of
// the generating view actually differ (the animation is visible).
func TestGeneratingRendersDistinctFrames(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newTestModel()
	m.phase = phaseWorking
	m.jobID = "job_x"
	m.status = "running"

	m.genFrame = 0
	f0 := m.workingView()
	m.genFrame = 3
	f3 := m.workingView()
	if f0 == f3 {
		t.Error("the generating animation should render differently across frames")
	}
}
