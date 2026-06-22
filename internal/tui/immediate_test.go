package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Framehood/framehood-cli/internal/mcp"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeTokens is a no-op Tokens for the in-test MCP client.
type fakeTokens struct{ tok string }

func (f *fakeTokens) Access() string                          { return f.tok }
func (f *fakeTokens) Refresh(context.Context) (string, error) { return f.tok, nil }

// toolCallServer returns an httptest server that answers every tools/call with a
// single MCP tool-call result whose first content item carries innerText
// verbatim. That mirrors the worker: read tools return their DATA as the content
// text, generation tools return a job envelope — both as the same shape.
func toolCallServer(t *testing.T, innerText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate the request contract — these read actions must go out as
		// tools/call, so a routing regression that sends a different method
		// (or none) fails the call instead of silently passing.
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Method != "tools/call" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"unexpected method %q"}}`, req.Method)
			return
		}
		// JSON-encode innerText so it is a valid JSON string in the "text" field.
		b, _ := json.Marshal(innerText)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%s}],"isError":false}}`, string(b))
	}))
}

// runCmd executes a tea.Cmd (possibly a Batch) and feeds the resulting message
// of type T into Update, returning the updated model. It ignores non-T messages
// (e.g. the spinner tick batched alongside the real result).
func drive[T tea.Msg](t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	msgs := flatten(cmd())
	for _, msg := range msgs {
		if _, ok := msg.(T); ok {
			nm, _ := m.Update(msg)
			return nm.(model)
		}
	}
	t.Fatalf("no message of expected type produced (got %v)", msgs)
	return m
}

// flatten runs a (possibly batched) message into a flat list of leaf messages.
func flatten(msg tea.Msg) []tea.Msg {
	if bm, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range bm {
			if c == nil {
				continue
			}
			out = append(out, flatten(c())...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func signedInModel(c *mcp.Client) model {
	m := newTestModel()
	m.client = c
	m.loggedIn = true
	return m
}

// TestImmediateRead_RendersListData is the core regression test: selecting an
// immediate read action whose tool returns LIST DATA (not a job envelope) must
// render that data with NO "decode job" error and leave the studio usable.
//
// Against the OLD buggy code this FAILS: the list silently decodes into an empty
// Job, handleJob schedules an endless poll on an empty job_id, and the studio is
// wedged in phaseWorking forever (never phaseDone, no readData).
func TestImmediateRead_RendersListData(t *testing.T) {
	srv := toolCallServer(t, `{"items":[{"name":"a.mp4"},{"name":"b.jpg"}]}`)
	defer srv.Close()
	m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))

	files := paletteCmdByID(t, "files·list")
	nm, cmd := m.runPaletteCmd(&files)
	got := drive[immediateResultMsg](t, nm.(model), cmd)

	if got.phase == phaseWorking {
		t.Fatal("studio stuck in phaseWorking after an immediate read (the freeze bug)")
	}
	if got.phase != phaseDone {
		t.Fatalf("phase = %v, want phaseDone", got.phase)
	}
	if got.errMsg != "" {
		t.Fatalf("immediate read produced an error: %q (the 'decode job' bug)", got.errMsg)
	}
	if strings.Contains(got.readData, "decode job") {
		t.Fatalf("a 'decode job' error leaked into the output: %q", got.readData)
	}
	if !strings.Contains(got.readData, "a.mp4") || !strings.Contains(got.readData, "b.jpg") {
		t.Fatalf("read data not rendered: %q", got.readData)
	}
	if got.jobID != "" {
		t.Errorf("an immediate read must not set a jobID (it is not a pollable job), got %q", got.jobID)
	}

	// The studio must stay usable: a subsequent key must not panic and must not
	// leave the model stuck working.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("keypress after an immediate read panicked: %v", r)
		}
	}()
	after, _ := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	am := after.(model)
	if am.phase == phaseWorking {
		t.Error("Esc after an immediate read must not enter phaseWorking")
	}
	// Esc dismisses the read panel back to a clean idle compose state.
	if am.readData != "" {
		t.Error("Esc should dismiss the read result panel")
	}
}

// TestImmediateRead_NeverStuckWorking drives the WHOLE command/message loop a
// read action produces and asserts the studio never ends up parked in
// phaseWorking. This is the direct freeze-symptom test, independent of which
// message type carries the result.
//
// Against the OLD code the list payload decodes into an EMPTY job, handleJob
// sees a non-terminal job with an empty id and schedules an endless poll — the
// model stays phaseWorking forever. Here that surfaces as: after running every
// message the command produced, phase is still phaseWorking.
func TestImmediateRead_NeverStuckWorking(t *testing.T) {
	srv := toolCallServer(t, `{"items":[1,2,3]}`)
	defer srv.Close()
	m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))

	files := paletteCmdByID(t, "files·list")
	nm, cmd := m.runPaletteCmd(&files)
	cur := nm.(model)
	if cmd == nil {
		t.Fatal("expected a command")
	}
	// Run every leaf message the command produced through Update.
	for _, msg := range flatten(cmd()) {
		next, _ := cur.Update(msg)
		cur = next.(model)
	}
	if cur.phase == phaseWorking {
		t.Fatal("studio stuck in phaseWorking after an immediate read — the freeze bug")
	}
	if cur.jobID != "" {
		t.Errorf("immediate read left a jobID %q (would drive an endless poll)", cur.jobID)
	}
}

// TestImmediateRead_RendersBareString covers the OTHER failing shape: a read tool
// that returns a bare JSON string (e.g. org·info). Against the OLD code this is
// the literal reported error: "decode job: json: cannot unmarshal string into Go
// value of type mcp.Job".
func TestImmediateRead_RendersBareString(t *testing.T) {
	srv := toolCallServer(t, `"Framehood — 3 members, owner kirill@framehood.ai"`)
	defer srv.Close()
	m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))

	org := paletteCmdByID(t, "org·info")
	nm, cmd := m.runPaletteCmd(&org)
	got := drive[immediateResultMsg](t, nm.(model), cmd)

	if got.phase != phaseDone || got.errMsg != "" {
		t.Fatalf("bare-string read failed: phase=%v err=%q", got.phase, got.errMsg)
	}
	if !strings.Contains(got.readData, "3 members") {
		t.Fatalf("bare-string read data not rendered: %q", got.readData)
	}
	// A bare JSON string is shown unquoted for readability.
	if strings.HasPrefix(got.readData, `"`) {
		t.Errorf("bare string should be unquoted in the panel, got %q", got.readData)
	}
}

// TestAllImmediateReads_RouteThroughCallTool verifies EVERY immediate read action
// (billing·balance/plans/plan, files·list, org·info/members/spend) is routed via
// the raw immediateCmd path — none of them touches Submit's Job decode. Each must
// land in phaseDone with the data rendered and no decode error.
func TestAllImmediateReads_RouteThroughCallTool(t *testing.T) {
	// One server reused for all reads; the payload is a generic object that is NOT
	// a valid job — the whole point is that it must render anyway.
	srv := toolCallServer(t, `{"ok":true,"value":42}`)
	defer srv.Close()

	var immediate []paletteCmd
	for _, c := range allPaletteCmds {
		if c.spec != nil && c.spec.immediate {
			immediate = append(immediate, c)
		}
	}
	if len(immediate) == 0 {
		t.Fatal("expected some immediate read actions in the catalog")
	}

	for _, c := range immediate {
		c := c
		t.Run(c.id, func(t *testing.T) {
			m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))
			nm, cmd := m.runPaletteCmd(&c)
			got := drive[immediateResultMsg](t, nm.(model), cmd)
			if got.phase != phaseDone {
				t.Fatalf("%s: phase=%v want phaseDone (stuck working / Job path?)", c.id, got.phase)
			}
			if got.errMsg != "" {
				t.Fatalf("%s: decode/other error leaked: %q", c.id, got.errMsg)
			}
			if !strings.Contains(got.readData, "42") {
				t.Fatalf("%s: data not rendered: %q", c.id, got.readData)
			}
			if got.jobID != "" {
				t.Errorf("%s: must not set a jobID", c.id)
			}
		})
	}
}

// TestImmediateRead_ErrorRecovers verifies a read that returns a tool-level error
// surfaces a RECOVERABLE notice — not a frozen studio.
func TestImmediateRead_ErrorRecovers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Error: rate limited"}],"isError":true}}`)
	}))
	defer srv.Close()
	m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))

	bal := paletteCmdByID(t, "billing·balance")
	nm, cmd := m.runPaletteCmd(&bal)
	got := drive[immediateResultMsg](t, nm.(model), cmd)

	if got.phase == phaseWorking {
		t.Fatal("a read error must not leave the studio in phaseWorking")
	}
	if got.phase != phaseError || got.errMsg == "" {
		t.Fatalf("expected a recoverable error notice, got phase=%v err=%q", got.phase, got.errMsg)
	}
	if got.focus != zoneInput {
		t.Error("after a read error the input must be focusable (focus zoneInput)")
	}
	// Enter on the empty prompt dismisses the error back to a clean compose state.
	after, _ := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	am := after.(model)
	if am.phase == phaseError || am.errMsg != "" {
		t.Error("Enter on empty prompt should dismiss the error")
	}
	if am.phase == phaseWorking {
		t.Error("dismissing an error must not enter phaseWorking")
	}
}

// TestGenerationJob_StillUsesJobPath is the counterpart: a GENERATION tool
// (image·create) whose response IS a job envelope still flows through the
// Submit/Job path unchanged — proving the fix didn't break generation.
func TestGenerationJob_StillUsesJobPath(t *testing.T) {
	// A terminal succeeded job with an image_url output.
	job := `{"job_id":"job_123","kind":"image","status":"succeeded","done":true,"outputs":{"image_url":"https://cdn.framehood.ai/job_123.jpg"}}`
	srv := toolCallServer(t, job)
	defer srv.Close()
	m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))
	m.focus = zoneInput
	m.action = findAction(t, "image", "create")
	m.input.SetValue("a red fox in the snow")

	nm, cmd := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	working := nm.(model)
	if working.phase != phaseWorking {
		t.Fatal("a runnable generation should enter phaseWorking")
	}
	// Feed the submittedMsg (the job) through Update.
	got := drive[submittedMsg](t, working, cmd)
	if got.phase != phaseDone {
		t.Fatalf("generation job: phase=%v want phaseDone", got.phase)
	}
	if got.result != "https://cdn.framehood.ai/job_123.jpg" {
		t.Fatalf("generation job result = %q, want the image_url", got.result)
	}
	if got.errMsg != "" {
		t.Fatalf("generation job produced an error: %q", got.errMsg)
	}
	// The generation result is a media URL, not read data.
	if got.readData != "" {
		t.Error("a generation must not populate readData")
	}
}

// TestSubmitError_Recovers verifies a Submit-time error (generation) recovers to
// a usable compose state rather than stranding the user in phaseWorking.
func TestSubmitError_Recovers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"Error: content policy"}],"isError":true}}`)
	}))
	defer srv.Close()
	m := signedInModel(mcp.New(srv.URL, &fakeTokens{tok: "tok"}))
	m.focus = zoneInput
	m.action = findAction(t, "image", "create")
	m.input.SetValue("something")

	nm, cmd := m.updateInput(tea.KeyMsg{Type: tea.KeyEnter})
	got := drive[submittedMsg](t, nm.(model), cmd)
	if got.phase == phaseWorking {
		t.Fatal("a submit error must not leave the studio in phaseWorking")
	}
	if got.phase != phaseError || got.errMsg == "" {
		t.Fatalf("expected a recoverable error, got phase=%v err=%q", got.phase, got.errMsg)
	}
	if got.focus != zoneInput {
		t.Error("after a submit error the input must be focusable")
	}
}
