# Frequency Change & Contact Tower Revamp — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `FC` require a frequency argument, make `TO` conditional on single-tower airports, preserve spoken frequency through STT, disambiguate shared frequencies across scenario controllers, and render readbacks that reflect facility-crossing semantics.

**Architecture:** Three layers touched in order (inside-out TDD): (1) pure helpers (`Frequency.StringSpoken`, STT `frequencyParser`); (2) intent types (new `UnknownFrequencyIntent`, new fields on `ContactIntent`/`ContactTowerIntent`); (3) command plumbing (`Sim.resolveControllerByFrequency`, updated `FC`/`TO` handlers and aircraft-side signatures); (4) STT grammar wired to emit frequencies and position hints.

**Tech Stack:** Go, cgo, Go `testing` package (standard `go test`). STT registry pattern in `stt/registry.go`. Intent render pattern in `aviation/intent.go` using `RadioTransmission.Add`.

Spec: `docs/superpowers/specs/2026-04-18-frequency-change-readback-design.md`

---

## File structure

**New files:** none.

**Modified files:**

| File | Responsibility after change |
|---|---|
| `aviation/aviation.go` | Adds `Frequency.StringSpoken()` formatter. |
| `aviation/aviation_test.go` | Unit tests for `StringSpoken`. |
| `aviation/intent.go` | New `UnknownFrequencyIntent`; new fields on `ContactIntent` and `ContactTowerIntent`; updated render templates. |
| `aviation/intent_test.go` | Render tests for the three intents' new branches. |
| `sim/errors.go` | New error sentinels: `ErrNoTowerForAirport`, `ErrAmbiguousTower`, `ErrFrequencyNotTower`. |
| `sim/aircraft.go` | `Aircraft.ContactTower` takes `target *av.Controller`, `freq av.Frequency`, `positionOnly bool`; populates intent accordingly. |
| `sim/control.go` | New `resolveControllerByFrequency` helper, new `towersForAirport` helper, updated `Sim.ContactTower`, new `Sim.FrequencyChange`, updated `FC` and `TO` command parsers. |
| `sim/control_test.go` *(may not exist — create if needed)* | Resolution helper tests and tower-count tests. |
| `stt/typeparsers.go` | Replace `contactFrequencyParser` with `frequencyParser` returning `av.Frequency`. |
| `stt/typeparsers_test.go` | Unit tests for the new parser. |
| `stt/handlers.go` | Replace the `contact`/`tower` pattern block with the spec's table. |
| `stt/handlers_test.go` *(or existing STT test file)* | End-to-end STT → command-string tests. |

---

## Ordering constraints

Tasks are ordered so the build + tests stay green after every commit:

1. Pure helpers first (Tasks 1–2) — add callable-but-uncalled utilities.
2. Intent type/field additions use defaulted zero-values so render stays backward-compatible (Tasks 3–5).
3. Resolver helpers added as uncalled functions (Tasks 6–8).
4. `Aircraft.ContactTower` and `Sim.ContactTower` signature update in one commit since they're coupled (Task 9).
5. New `Sim.FrequencyChange` method (Task 10) — not wired yet.
6. Command-parser `FC`/`TO` updates accept *both* old and new forms in one commit (Task 11) — STT can still emit old form.
7. STT frequency parser + pattern table replace `contactFrequencyParser` (Task 12) — emits new form.
8. Remove bare `FC` compatibility (Task 13) — only run after STT emits freq form exclusively.
9. Integration tests + cleanup (Task 14).

---

### Task 1: `Frequency.StringSpoken()` formatter

**Files:**
- Modify: `aviation/aviation.go:82-88`
- Test: `aviation/aviation_test.go`

- [ ] **Step 1.1: Write failing tests**

Add to `aviation/aviation_test.go` (append to file; if file doesn't define `TestFrequencyStringSpoken`, add it):

```go
func TestFrequencyStringSpoken(t *testing.T) {
    tests := []struct {
        val  Frequency
        want string
    }{
        {127750, "127.75"},
        {118300, "118.3"},
        {134000, "134.0"},
        {127755, "127.755"},
        {118000, "118.0"},
        {136975, "136.975"},
    }
    for _, tc := range tests {
        got := tc.val.StringSpoken()
        if got != tc.want {
            t.Errorf("Frequency(%d).StringSpoken() = %q, want %q", tc.val, got, tc.want)
        }
    }
}
```

- [ ] **Step 1.2: Run test, verify it fails**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestFrequencyStringSpoken
```

Expected: `FAIL` with `Frequency.StringSpoken undefined`.

- [ ] **Step 1.3: Implement**

In `aviation/aviation.go` after the existing `Frequency.String()` (around line 88):

```go
// StringSpoken renders the frequency for speech/readback: trims trailing
// zeros from the fractional part but keeps at least one fractional digit.
// Example: Frequency(127750).StringSpoken() == "127.75".
func (f Frequency) StringSpoken() string {
    whole := int(f / 1000)
    frac := int(f % 1000)
    if frac == 0 {
        return fmt.Sprintf("%d.0", whole)
    }
    s := fmt.Sprintf("%03d", frac)
    s = strings.TrimRight(s, "0")
    return fmt.Sprintf("%d.%s", whole, s)
}
```

Add `"strings"` to the file's imports if not already present.

- [ ] **Step 1.4: Run tests**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestFrequencyStringSpoken -v
```

Expected: `PASS`.

- [ ] **Step 1.5: Commit**

```bash
git add aviation/aviation.go aviation/aviation_test.go
git commit -m "aviation: Frequency.StringSpoken() for readback rendering"
```

---

### Task 2: New STT `frequencyParser`

**Files:**
- Modify: `stt/typeparsers.go:947-1004`
- Test: `stt/typeparsers_test.go` (create if absent; check first with `ls stt/typeparsers_test.go`)

**Note:** This replaces `contactFrequencyParser`. The old parser's two call sites in `stt/handlers.go` will be updated in Task 12; until then the old parser stays in place under its old name, and the new parser is added alongside.

- [ ] **Step 2.1: Add parser type (keep old one for now)**

Append to `stt/typeparsers.go` after the `contactFrequencyParser` block:

```go
// frequencyParser matches a spoken frequency and returns it as av.Frequency
// (kHz ×1000). Accepted shapes:
//   - "N N N point N N"   → e.g. 127 point 75  → Frequency(127750)
//   - "N N N point N N N" → e.g. 127 point 750 → Frequency(127750)
//   - "N N N N N"         → 5 digits, trailing zero implicit → Frequency(127750)
//   - "N N N N N N"       → 6 digits explicit → Frequency(127750)
// Rejects values outside 118000..137000.
type frequencyParser struct{}

func (p *frequencyParser) identifier() string { return "frequency" }
func (p *frequencyParser) goType() reflect.Type {
    return reflect.TypeOf(av.Frequency(0))
}

func (p *frequencyParser) parse(tokens []Token, pos int, ac Aircraft) (any, int, string) {
    if pos >= len(tokens) {
        return nil, 0, ""
    }
    // Collect up to 10 consecutive digit/point tokens starting at pos.
    maxLookahead := min(10, len(tokens)-pos)
    var digits []int
    sawPoint := false
    pointAt := -1
    consumed := 0
    for i := 0; i < maxLookahead; i++ {
        t := tokens[pos+i]
        if t.Type == TokenNumber && t.Value >= 0 && t.Value <= 9 {
            digits = append(digits, t.Value)
            consumed = i + 1
            continue
        }
        if t.Type == TokenWord && strings.ToLower(t.Text) == "point" && !sawPoint {
            sawPoint = true
            pointAt = len(digits)
            consumed = i + 1
            continue
        }
        // Also accept multi-digit number tokens like "127" or "75" as a
        // sequence of digits (the tokenizer may aggregate some spoken numbers).
        if t.Type == TokenNumber && t.Value >= 10 {
            // Expand into digits.
            v := t.Value
            var tmp []int
            for v > 0 {
                tmp = append([]int{v % 10}, tmp...)
                v /= 10
            }
            digits = append(digits, tmp...)
            consumed = i + 1
            continue
        }
        break
    }

    // Need at least 5 digits total.
    if len(digits) < 5 || len(digits) > 6 {
        return nil, 0, ""
    }

    // If "point" was present, enforce 3 digits before it and 2 or 3 after.
    if sawPoint {
        if pointAt != 3 {
            return nil, 0, ""
        }
        after := len(digits) - 3
        if after != 2 && after != 3 {
            return nil, 0, ""
        }
    }

    // Assemble kHz value. 5 digits → append trailing 0; 6 digits → as-is.
    khz := 0
    for _, d := range digits {
        khz = khz*10 + d
    }
    if len(digits) == 5 {
        khz *= 10
    }

    if khz < 118000 || khz > 137000 {
        return nil, 0, ""
    }
    return av.Frequency(khz), consumed, ""
}
```

Ensure `stt/typeparsers.go` already imports `av "github.com/mmp/vice/aviation"` (or the equivalent alias used in that file). If the import is absent, add it.

- [ ] **Step 2.2: Write unit tests**

Create or append to `stt/typeparsers_test.go`:

```go
func TestFrequencyParser(t *testing.T) {
    p := &frequencyParser{}
    cases := []struct {
        name   string
        tokens []Token
        want   av.Frequency
        cons   int
    }{
        {
            name: "point_two_digit",
            tokens: []Token{
                {Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
                {Type: TokenNumber, Value: 7}, {Type: TokenWord, Text: "point"},
                {Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 5},
            },
            want: 127750, cons: 6,
        },
        {
            name: "point_three_digit",
            tokens: []Token{
                {Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
                {Type: TokenNumber, Value: 7}, {Type: TokenWord, Text: "point"},
                {Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 5},
                {Type: TokenNumber, Value: 0},
            },
            want: 127750, cons: 7,
        },
        {
            name: "five_digits_no_point",
            tokens: []Token{
                {Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
                {Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 7},
                {Type: TokenNumber, Value: 5},
            },
            want: 127750, cons: 5,
        },
        {
            name: "six_digits_no_point",
            tokens: []Token{
                {Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 2},
                {Type: TokenNumber, Value: 7}, {Type: TokenNumber, Value: 7},
                {Type: TokenNumber, Value: 5}, {Type: TokenNumber, Value: 0},
            },
            want: 127750, cons: 6,
        },
        {
            name: "out_of_band_rejected",
            tokens: []Token{
                {Type: TokenNumber, Value: 1}, {Type: TokenNumber, Value: 0},
                {Type: TokenNumber, Value: 0}, {Type: TokenWord, Text: "point"},
                {Type: TokenNumber, Value: 0}, {Type: TokenNumber, Value: 0},
            },
            want: 0, cons: 0,
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, cons, _ := p.parse(tc.tokens, 0, Aircraft{})
            if tc.want == 0 {
                if got != nil {
                    t.Errorf("expected rejection, got %v", got)
                }
                return
            }
            f, ok := got.(av.Frequency)
            if !ok {
                t.Fatalf("got %T, want av.Frequency", got)
            }
            if f != tc.want {
                t.Errorf("freq = %d, want %d", f, tc.want)
            }
            if cons != tc.cons {
                t.Errorf("consumed = %d, want %d", cons, tc.cons)
            }
        })
    }
}
```

- [ ] **Step 2.3: Run tests**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./stt/ -run TestFrequencyParser -v
```

Expected: `PASS`.

- [ ] **Step 2.4: Register the parser**

Find the parser-registration block in `stt/typeparsers.go` (search for where `contactFrequencyParser{}` is added to any registry/map). Add a line registering `&frequencyParser{}` alongside it. If no registry exists and parsers are referenced by identifier string in a lookup function, add a case for `"frequency"` that returns `&frequencyParser{}`.

```bash
grep -n "contactFrequencyParser" stt/typeparsers.go
```

Mirror whatever pattern registers `contactFrequencyParser` for the new parser.

- [ ] **Step 2.5: Verify build**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./stt/...
```

Expected: no errors.

- [ ] **Step 2.6: Commit**

```bash
git add stt/typeparsers.go stt/typeparsers_test.go
git commit -m "stt: frequencyParser returning av.Frequency for FC/TO arguments"
```

---

### Task 3: `UnknownFrequencyIntent`

**Files:**
- Modify: `aviation/intent.go` (after `ContactTowerIntent` around line 855)
- Test: `aviation/intent_test.go`

- [ ] **Step 3.1: Write failing test**

Append to `aviation/intent_test.go`:

```go
func TestUnknownFrequencyIntentRenders(t *testing.T) {
    intent := UnknownFrequencyIntent{Frequency: 127750}
    rt := &RadioTransmission{}
    r := rand.New(rand.NewSource(1))
    intent.Render(rt, r)
    s := rt.String()
    // Must contain "frequency" or "say again" or similar.
    if !strings.Contains(strings.ToLower(s), "frequency") &&
        !strings.Contains(strings.ToLower(s), "say again") &&
        !strings.Contains(strings.ToLower(s), "nothing") {
        t.Errorf("render missing expected phrase: %q", s)
    }
}
```

- [ ] **Step 3.2: Run, verify failure**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestUnknownFrequencyIntent
```

Expected: `UnknownFrequencyIntent undefined`.

- [ ] **Step 3.3: Add intent type and render**

In `aviation/intent.go`, after `ContactTowerIntent.Render` (current line ~855):

```go
// UnknownFrequencyIntent is returned when the controller issued a handoff to
// a frequency that does not resolve to any known controller. The aircraft
// does NOT switch frequencies; it verbally asks for the correct freq.
type UnknownFrequencyIntent struct {
    Frequency Frequency
}

func (u UnknownFrequencyIntent) Render(rt *RadioTransmission, r *rand.Rand) {
    rt.Add("[what was that frequency?|"+
        "we hear nothing on {freq}, what was the frequency?|"+
        "say again the frequency?|"+
        "nothing heard on {freq}, say again?]", u.Frequency)
}
```

If `{freq}` placeholder binding in `rt.Add` uses `Frequency.String()` today, update it to use the spoken form — see Task 5 for the render-engine side if the token is shared. For now, the templates above should already resolve `{freq}` to whatever the current helper produces.

- [ ] **Step 3.4: Run test**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestUnknownFrequencyIntent -v
```

Expected: `PASS`.

- [ ] **Step 3.5: Commit**

```bash
git add aviation/intent.go aviation/intent_test.go
git commit -m "aviation: UnknownFrequencyIntent for unresolved handoff freq"
```

---

### Task 4: `ContactTowerIntent` field additions + render variants

**Files:**
- Modify: `aviation/intent.go:850-855`
- Test: `aviation/intent_test.go`

- [ ] **Step 4.1: Write failing tests**

Append to `aviation/intent_test.go`:

```go
func TestContactTowerIntentPositionOnly(t *testing.T) {
    intent := ContactTowerIntent{PositionOnly: true}
    rt := &RadioTransmission{}
    intent.Render(rt, rand.New(rand.NewSource(1)))
    got := strings.ToLower(rt.String())
    if !strings.Contains(got, "tower") {
        t.Errorf("want 'tower' in output, got %q", got)
    }
    // Position-only path must NOT mention a numeric frequency.
    if strings.Contains(got, ".") {
        t.Errorf("position-only readback should not include a frequency: %q", got)
    }
}

func TestContactTowerIntentWithFrequency(t *testing.T) {
    ctrl := &Controller{Callsign: "ORL_TWR", RadioName: "Orlando Tower", Frequency: 124300}
    intent := ContactTowerIntent{ToController: ctrl, Frequency: 124300}
    rt := &RadioTransmission{}
    intent.Render(rt, rand.New(rand.NewSource(1)))
    got := strings.ToLower(rt.String())
    if !strings.Contains(got, "124.3") {
        t.Errorf("want frequency '124.3' in output, got %q", got)
    }
}
```

- [ ] **Step 4.2: Run, verify failure**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestContactTowerIntent -v
```

Expected: compilation errors on the new fields.

- [ ] **Step 4.3: Update struct and render**

Replace the existing `ContactTowerIntent` definition (around line 850) with:

```go
// ContactTowerIntent represents contact tower command.
// PositionOnly is true only on the STT bare-tower path (and only valid at
// airports with exactly one tower). All other paths supply ToController and
// Frequency and read back position + frequency.
type ContactTowerIntent struct {
    ToController *Controller
    Frequency    Frequency
    PositionOnly bool
}

func (c ContactTowerIntent) Render(rt *RadioTransmission, r *rand.Rand) {
    if c.PositionOnly {
        rt.Add("[contact|over to|] tower")
        return
    }
    rt.Add("[contact|over to|] {actrl} on {freq}, [good day|seeya|]", c.ToController, c.Frequency)
}
```

- [ ] **Step 4.4: Run tests**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestContactTowerIntent -v
```

Expected: `PASS`.

- [ ] **Step 4.5: Update call sites**

The only current construction is `av.ContactTowerIntent{}` in `sim/aircraft.go:477` and `:484`. Both use position-only behavior today. Make that explicit:

```bash
grep -n "ContactTowerIntent{" sim/aircraft.go
```

Change each `av.ContactTowerIntent{}` to `av.ContactTowerIntent{PositionOnly: true}` as a temporary bridge — Task 9 will update these properly.

- [ ] **Step 4.6: Verify build**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./...
```

Expected: no errors.

- [ ] **Step 4.7: Commit**

```bash
git add aviation/intent.go aviation/intent_test.go sim/aircraft.go
git commit -m "aviation: ContactTowerIntent carries target+freq; position-only flag"
```

---

### Task 5: `ContactIntent.SameFacility` + render variants

**Files:**
- Modify: `aviation/intent.go:791-811`
- Test: `aviation/intent_test.go`

- [ ] **Step 5.1: Write failing tests**

Append to `aviation/intent_test.go`:

```go
func TestContactIntentSameFacilityFreqOnly(t *testing.T) {
    ctrl := &Controller{Callsign: "MCO_APP", RadioName: "Orlando Approach", Frequency: 127750, Facility: "MCO"}
    intent := ContactIntent{
        Type:         ContactController,
        ToController: ctrl,
        Frequency:    127750,
        SameFacility: true,
    }
    rt := &RadioTransmission{}
    intent.Render(rt, rand.New(rand.NewSource(1)))
    got := strings.ToLower(rt.String())
    if !strings.Contains(got, "127.75") {
        t.Errorf("expected '127.75' in same-facility readback, got %q", got)
    }
    if strings.Contains(got, "orlando") || strings.Contains(got, "approach") {
        t.Errorf("same-facility readback should omit position, got %q", got)
    }
}

func TestContactIntentCrossFacilityFull(t *testing.T) {
    ctrl := &Controller{Callsign: "ZJX_N56", RadioName: "Jacksonville Center", Frequency: 134000, Facility: "ZJX"}
    intent := ContactIntent{
        Type:         ContactController,
        ToController: ctrl,
        Frequency:    134000,
        SameFacility: false,
    }
    rt := &RadioTransmission{}
    intent.Render(rt, rand.New(rand.NewSource(1)))
    got := strings.ToLower(rt.String())
    if !strings.Contains(got, "134.0") {
        t.Errorf("want '134.0' in cross-facility readback, got %q", got)
    }
    if !strings.Contains(got, "jacksonville") && !strings.Contains(got, "center") {
        t.Errorf("cross-facility readback should include position, got %q", got)
    }
}
```

- [ ] **Step 5.2: Run, verify failure**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -run TestContactIntent -v
```

Expected: field `SameFacility` undefined.

- [ ] **Step 5.3: Update struct and render**

Replace the current `ContactIntent` struct and `Render` (lines ~791–811) with:

```go
// ContactIntent represents contact/handoff commands.
// SameFacility is set by the dispatcher: true iff the aircraft's current
// controller and the target controller share a Facility. Typed-command paths
// pass false unconditionally so readbacks always include position+frequency.
type ContactIntent struct {
    Type         ContactType
    ToController *Controller
    Frequency    Frequency
    IsDeparture  bool
    SameFacility bool
}

func (c ContactIntent) Render(rt *RadioTransmission, r *rand.Rand) {
    switch c.Type {
    case ContactController:
        if c.SameFacility {
            rt.Add("[|that's ]{freq}, [good day|seeya|thanks|]", c.Frequency)
            return
        }
        if c.IsDeparture {
            rt.Add("[contact|over to|] {dctrl} on {freq}, [good day|seeya|]", c.ToController, c.Frequency)
        } else {
            rt.Add("[contact|over to|] {actrl} on {freq}, [good day|seeya|]", c.ToController, c.Frequency)
        }
    case ContactGoodbye:
        rt.Add("[goodbye|seeya]")
    case ContactRadarTerminated:
        rt.Add("[radar services terminated, seeya|radar services terminated, squawk VFR]")
    }
}
```

- [ ] **Step 5.4: Check that `{freq}` token uses `StringSpoken` form**

Find where `{freq}` is substituted in the radio-transmission rendering code:

```bash
grep -rn "\"{freq}\"\|'{freq}'\|case \"freq\"\|\"freq\":" aviation/
```

If the substitution currently uses `Frequency.String()` (full 6-char form), change it to `Frequency.StringSpoken()`. If that change breaks other callers, add a new token name like `{freq_spoken}` and use that in the new templates in this task + Tasks 3–4.

- [ ] **Step 5.5: Run all aviation tests**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./aviation/ -v
```

Expected: all PASS.

- [ ] **Step 5.6: Commit**

```bash
git add aviation/intent.go aviation/intent_test.go
git commit -m "aviation: ContactIntent.SameFacility toggles freq-only vs full readback"
```

---

### Task 6: Error sentinels

**Files:**
- Modify: `sim/errors.go`

- [ ] **Step 6.1: Add sentinels**

Insert alphabetically in the `var (...)` block:

```go
ErrAmbiguousTower       = errors.New("Multiple towers serve this airport; specify frequency")
ErrFrequencyNotTower    = errors.New("Frequency does not resolve to a tower controller for this airport")
ErrNoTowerForAirport    = errors.New("No tower controller configured for arrival airport")
```

- [ ] **Step 6.2: Verify build**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./...
```

- [ ] **Step 6.3: Commit**

```bash
git add sim/errors.go
git commit -m "sim: error sentinels for tower-handoff validation"
```

---

### Task 7: `towersForAirport` helper

**Files:**
- Modify: `sim/control.go` (add near other lookup helpers — search for `GetDepartureController` around line 993 and place after it)
- Test: `sim/control_test.go` (create if needed)

- [ ] **Step 7.1: Write failing test**

Create or append to `sim/control_test.go`:

```go
func TestTowersForAirport(t *testing.T) {
    s := &Sim{
        State: State{
            Controllers: map[TCP]*av.Controller{
                "IAD_N_TWR": {Callsign: "IAD_N_TWR", Frequency: 120100},
                "IAD_E_TWR": {Callsign: "IAD_E_TWR", Frequency: 120750},
                "IAD_W_TWR": {Callsign: "IAD_W_TWR", Frequency: 119850},
                "DCA_TWR":   {Callsign: "DCA_TWR", Frequency: 119100},
                "MCO_APP":   {Callsign: "MCO_APP", Frequency: 127750},
            },
        },
    }
    got := s.towersForAirport("IAD")
    if len(got) != 3 {
        t.Fatalf("IAD: got %d towers, want 3", len(got))
    }
    got = s.towersForAirport("DCA")
    if len(got) != 1 || got[0].Callsign != "DCA_TWR" {
        t.Errorf("DCA: got %+v, want 1 DCA_TWR", got)
    }
    got = s.towersForAirport("MCO")
    if len(got) != 0 {
        t.Errorf("MCO: got %d, want 0", len(got))
    }
}
```

- [ ] **Step 7.2: Run, verify failure**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./sim/ -run TestTowersForAirport
```

Expected: undefined method.

- [ ] **Step 7.3: Implement**

Add to `sim/control.go` (near `GetDepartureController` around line 993):

```go
// towersForAirport returns all controllers whose callsign matches the
// tower convention `<AIRPORT>_TWR` or `<AIRPORT>_<TAG>_TWR` (e.g. IAD_N_TWR).
func (s *Sim) towersForAirport(airport string) []*av.Controller {
    if airport == "" {
        return nil
    }
    prefix := airport + "_"
    var out []*av.Controller
    for _, c := range s.State.Controllers {
        if c == nil {
            continue
        }
        cs := c.Callsign
        if !strings.HasSuffix(cs, "_TWR") {
            continue
        }
        if !strings.HasPrefix(cs, prefix) {
            continue
        }
        // Either `<airport>_TWR` exactly, or `<airport>_<tag>_TWR`.
        middle := cs[len(prefix) : len(cs)-len("_TWR")]
        if middle != "" && strings.Contains(middle, "_") {
            // Disallow deeper nesting just to be safe.
            continue
        }
        out = append(out, c)
    }
    return out
}
```

Add `"strings"` to imports if not present.

- [ ] **Step 7.4: Run test**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./sim/ -run TestTowersForAirport -v
```

Expected: `PASS`.

- [ ] **Step 7.5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: towersForAirport enumerates towers by callsign convention"
```

---

### Task 8: `resolveControllerByFrequency` with layered tiebreaker

**Files:**
- Modify: `sim/control.go`
- Test: `sim/control_test.go`

- [ ] **Step 8.1: Write failing tests**

Append to `sim/control_test.go`:

```go
func TestResolveControllerByFrequency_ZeroMatches(t *testing.T) {
    s := &Sim{State: State{Controllers: map[TCP]*av.Controller{
        "A": {Callsign: "A", Frequency: 127750},
    }}}
    ac := &Aircraft{}
    ctrl, err := s.resolveControllerByFrequency(ac, 135000, "")
    if ctrl != nil || err == nil {
        t.Errorf("want (nil, err), got (%v, %v)", ctrl, err)
    }
}

func TestResolveControllerByFrequency_UniqueMatch(t *testing.T) {
    target := &av.Controller{Callsign: "A", Frequency: 127750}
    s := &Sim{State: State{Controllers: map[TCP]*av.Controller{"A": target}}}
    ac := &Aircraft{}
    ctrl, err := s.resolveControllerByFrequency(ac, 127750, "")
    if err != nil || ctrl != target {
        t.Errorf("want target, got (%v, %v)", ctrl, err)
    }
}

func TestResolveControllerByFrequency_NameHintWins(t *testing.T) {
    a := &av.Controller{Callsign: "X", RadioName: "Orlando Approach", Frequency: 127750, Facility: "MCO"}
    b := &av.Controller{Callsign: "Y", RadioName: "Tampa Approach", Frequency: 127750, Facility: "TPA"}
    s := &Sim{State: State{Controllers: map[TCP]*av.Controller{"X": a, "Y": b}}}
    ac := &Aircraft{}
    ctrl, err := s.resolveControllerByFrequency(ac, 127750, "orlando")
    if err != nil || ctrl != a {
        t.Errorf("want Orlando, got (%v, %v)", ctrl, err)
    }
}

func TestResolveControllerByFrequency_OutOfBandError(t *testing.T) {
    s := &Sim{State: State{Controllers: map[TCP]*av.Controller{}}}
    ac := &Aircraft{}
    _, err := s.resolveControllerByFrequency(ac, 99000, "")
    if err == nil {
        t.Errorf("want err for out-of-band freq, got nil")
    }
}
```

- [ ] **Step 8.2: Run, verify failure**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./sim/ -run TestResolveControllerByFrequency
```

- [ ] **Step 8.3: Implement**

Add to `sim/control.go` (after `towersForAirport`):

```go
// resolveControllerByFrequency picks the best matching controller for the
// given frequency across the full scenario controller set. On multi-match,
// applies the layered tiebreaker D1 (name hint), D2 (facility adjacency),
// D3 (phase of flight). Returns (nil, ErrInvalidFrequency) on zero matches.
func (s *Sim) resolveControllerByFrequency(ac *Aircraft, freq av.Frequency, positionHint string) (*av.Controller, error) {
    if freq < 118000 || freq > 137000 {
        return nil, ErrInvalidFrequency
    }
    var candidates []*av.Controller
    for _, c := range s.State.Controllers {
        if c != nil && c.Frequency == freq {
            candidates = append(candidates, c)
        }
    }
    if len(candidates) == 0 {
        return nil, ErrInvalidFrequency
    }
    if len(candidates) == 1 {
        return candidates[0], nil
    }

    // D1: name hint filter.
    if positionHint != "" {
        hint := strings.ToLower(strings.TrimSpace(positionHint))
        filtered := candidates[:0:0]
        for _, c := range candidates {
            if strings.Contains(strings.ToLower(c.RadioName), hint) ||
                strings.Contains(strings.ToLower(c.Callsign), hint) {
                filtered = append(filtered, c)
            }
        }
        if len(filtered) > 0 {
            candidates = filtered
        }
    }
    if len(candidates) == 1 {
        return candidates[0], nil
    }

    // D2: facility adjacency.
    fromFacility := ""
    if ac != nil {
        if fromCtrl := s.controllerForAircraft(ac); fromCtrl != nil {
            fromFacility = fromCtrl.Facility
        }
    }
    if fromFacility != "" {
        filtered := candidates[:0:0]
        for _, c := range candidates {
            if c.Facility == fromFacility {
                filtered = append(filtered, c)
            }
        }
        if len(filtered) > 0 {
            candidates = filtered
        }
    }
    if len(candidates) == 1 {
        return candidates[0], nil
    }

    // D3: phase of flight.
    filtered := candidates[:0:0]
    if ac != nil && ac.Nav.Approach.Cleared {
        prefix := ac.FlightPlan.ArrivalAirport + "_"
        for _, c := range candidates {
            if strings.HasPrefix(c.Callsign, prefix) {
                filtered = append(filtered, c)
            }
        }
    } else {
        for _, c := range candidates {
            if c.ERAMFacility {
                filtered = append(filtered, c)
            }
        }
    }
    if len(filtered) > 0 {
        candidates = filtered
    }

    // Deterministic fallback.
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Callsign < candidates[j].Callsign
    })
    if len(candidates) > 1 {
        s.lg.Warnf("ambiguous frequency %s resolved to %s; candidates: %v",
            freq.StringSpoken(), candidates[0].Callsign, callsignList(candidates))
    }
    return candidates[0], nil
}

func callsignList(cs []*av.Controller) []string {
    out := make([]string, len(cs))
    for i, c := range cs {
        out[i] = c.Callsign
    }
    return out
}
```

If `controllerForAircraft` doesn't exist, add a helper:

```go
func (s *Sim) controllerForAircraft(ac *Aircraft) *av.Controller {
    if ac == nil {
        return nil
    }
    if c, ok := s.State.Controllers[TCP(ac.ControllerFrequency)]; ok {
        return c
    }
    return nil
}
```

Add `"sort"` to imports. Ensure `ErrInvalidFrequency` exists — if not, add it to `sim/errors.go`:

```go
ErrInvalidFrequency = errors.New("Frequency does not resolve to any controller")
```

- [ ] **Step 8.4: Run tests**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./sim/ -run TestResolveControllerByFrequency -v
```

Expected: `PASS`.

- [ ] **Step 8.5: Commit**

```bash
git add sim/control.go sim/control_test.go sim/errors.go
git commit -m "sim: resolveControllerByFrequency with layered tiebreaker"
```

---

### Task 9: Update `Aircraft.ContactTower` + `Sim.ContactTower` signatures

**Files:**
- Modify: `sim/aircraft.go:469-486`
- Modify: `sim/control.go:2056-2068` and callers at `:3939`, `:4141`

These are coupled — signature changes must land in one commit.

- [ ] **Step 9.1: Update `Aircraft.ContactTower`**

Replace the existing function:

```go
// ContactTower produces a ContactTowerIntent if the aircraft is eligible.
// When target is nil and positionOnly is true, renders a position-only
// readback (STT bare-tower path, single-tower airport). Otherwise target
// must be non-nil and the readback includes position + frequency.
func (ac *Aircraft) ContactTower(target *av.Controller, freq av.Frequency, positionOnly bool, lg *log.Logger) (av.CommandIntent, bool) {
    if ac.GotContactTower {
        return nil, false
    }
    if ac.FlightPlan.Rules != av.FlightRulesVFR {
        if ac.Nav.Approach.Assigned == nil {
            return av.MakeUnableIntent("unable. We haven't been given an approach."), false
        }
        if !ac.Nav.Approach.Cleared {
            return av.MakeUnableIntent("unable. We haven't been cleared for the approach."), false
        }
    }
    ac.GotContactTower = true
    return av.ContactTowerIntent{
        ToController: target,
        Frequency:    freq,
        PositionOnly: positionOnly,
    }, true
}
```

- [ ] **Step 9.2: Update `Sim.ContactTower` signature and body**

Validation runs before `dispatchControlledAircraftCommand` so that tower-count errors surface as command errors (not silent nil-intents). Replace the existing `Sim.ContactTower` (around line 2056) with:

```go
func (s *Sim) ContactTower(tcw TCW, callsign av.ADSBCallsign, freq av.Frequency, positionHint string, fromTypedCommand bool) (av.CommandIntent, error) {
    s.mu.Lock(s.lg)
    defer s.mu.Unlock(s.lg)

    ac, ok := s.Aircraft[callsign]
    if !ok {
        return nil, ErrNoMatchingFlight
    }
    airport := ac.FlightPlan.ArrivalAirport
    towers := s.towersForAirport(airport)

    var target *av.Controller
    positionOnly := false

    if freq == 0 {
        switch len(towers) {
        case 0:
            return nil, ErrNoTowerForAirport
        case 1:
            target = towers[0]
            positionOnly = !fromTypedCommand
        default:
            return nil, ErrAmbiguousTower
        }
    } else {
        resolved, err := s.resolveControllerByFrequency(ac, freq, positionHint)
        if err != nil {
            // Zero-match → pilot-facing readback, not command error.
            return av.UnknownFrequencyIntent{Frequency: freq}, nil
        }
        prefix := airport + "_"
        if !strings.HasSuffix(resolved.Callsign, "_TWR") || !strings.HasPrefix(resolved.Callsign, prefix) {
            return nil, ErrFrequencyNotTower
        }
        target = resolved
    }

    return s.dispatchControlledAircraftCommand(tcw, callsign,
        func(tcw TCW, ac *Aircraft) av.CommandIntent {
            result, ok := ac.ContactTower(target, freq, positionOnly, s.lg)
            if ok {
                if target != nil {
                    ac.ControllerFrequency = ControlPosition(target.Callsign)
                } else {
                    ac.ControllerFrequency = "_TOWER"
                }
            }
            return result
        })
}
```

- [ ] **Step 9.3: Update existing callers of `Sim.ContactTower`**

Two call sites: `sim/control.go:3939` and `:4141`. Update both to pass `(tcw, callsign, 0, "", true)` — bare form from typed command:

```go
return s.ContactTower(tcw, callsign, 0, "", true)
```

Do not change their conditional logic yet — Task 11 rewrites the `FC`/`TO` branches.

- [ ] **Step 9.4: Update the bridge from Task 4**

In `sim/aircraft.go`, the `av.ContactTowerIntent{PositionOnly: true}` constructions from Task 4.5 should now flow through `ContactTower`'s new signature naturally. Confirm nothing else constructs `ContactTowerIntent{}` directly.

```bash
grep -rn "ContactTowerIntent{" sim/ aviation/ stt/
```

- [ ] **Step 9.5: Build + test**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./... && go test ./sim/ ./aviation/ -v
```

Expected: all PASS.

- [ ] **Step 9.6: Commit**

```bash
git add sim/aircraft.go sim/control.go
git commit -m "sim: ContactTower accepts freq+target, propagates to intent"
```

---

### Task 10: `Sim.FrequencyChange` method

**Files:**
- Modify: `sim/control.go`

- [ ] **Step 10.1: Add `FrequencyChange`**

`FrequencyChange` handles non-tower handoffs. The cleared-for-approach → tower routing is handled in the `FC` parser (Task 11): if the resolved target turns out to be a tower for the arrival airport, the parser calls `ContactTower` instead of `FrequencyChange`. That keeps this method simple and avoids a locked-re-entry into `ContactTower`.

Add next to `ContactTower`:

```go
// FrequencyChange dispatches a non-tower handoff by frequency. fromTypedCommand
// forces SameFacility=false so typed commands always produce full readback.
func (s *Sim) FrequencyChange(tcw TCW, callsign av.ADSBCallsign, freq av.Frequency, positionHint string, fromTypedCommand bool) (av.CommandIntent, error) {
    s.mu.Lock(s.lg)
    defer s.mu.Unlock(s.lg)

    ac, ok := s.Aircraft[callsign]
    if !ok {
        return nil, ErrNoMatchingFlight
    }

    target, err := s.resolveControllerByFrequency(ac, freq, positionHint)
    if err != nil {
        return av.UnknownFrequencyIntent{Frequency: freq}, nil
    }

    fromCtrl := s.controllerForAircraft(ac)
    sameFacility := !fromTypedCommand && fromCtrl != nil && fromCtrl.Facility == target.Facility

    return s.dispatchControlledAircraftCommand(tcw, callsign,
        func(tcw TCW, ac *Aircraft) av.CommandIntent {
            intent := av.ContactIntent{
                Type:         av.ContactController,
                ToController: target,
                Frequency:    freq,
                SameFacility: sameFacility,
            }
            ac.ControllerFrequency = ControlPosition(target.Callsign)
            return intent
        })
}
```

- [ ] **Step 10.2: Build + test**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./... && go test ./sim/ ./aviation/
```

- [ ] **Step 10.3: Commit**

```bash
git add sim/control.go
git commit -m "sim: FrequencyChange method dispatches FC via resolved controller"
```

---

### Task 11: Update `FC`/`TO` command parsers

**Files:**
- Modify: `sim/control.go:3934-3945` (`FC` case)
- Modify: `sim/control.go:4137-4141` (`TO` case)

- [ ] **Step 11.1: Update `FC` parser**

Replace the `case 'F':` block in the command switch with:

```go
case 'F':
    if strings.HasPrefix(command, "FC") && len(command) > 2 {
        // Format: FC{digits} or FC{digits}:{hint}
        rest := command[2:]
        var hint string
        if idx := strings.Index(rest, ":"); idx >= 0 {
            hint = rest[idx+1:]
            rest = rest[:idx]
        }
        freq, err := parseFrequencyDigits(rest)
        if err != nil {
            return nil, ErrInvalidCommandSyntax
        }
        fromTyped := hint == ""
        // If the resolved target turns out to be a tower for this aircraft's
        // arrival airport while the aircraft is cleared for approach, the
        // handler auto-routes through ContactTower for the correct gating.
        if ac, ok := s.Aircraft[callsign]; ok && ac.Nav.Approach.Cleared {
            if target, rerr := s.resolveControllerByFrequency(ac, freq, hint); rerr == nil {
                prefix := ac.FlightPlan.ArrivalAirport + "_"
                if strings.HasSuffix(target.Callsign, "_TWR") && strings.HasPrefix(target.Callsign, prefix) {
                    return s.ContactTower(tcw, callsign, freq, hint, fromTyped)
                }
            }
        }
        return s.FrequencyChange(tcw, callsign, freq, hint, fromTyped)
    }
    return nil, ErrInvalidCommandSyntax
```

Add a helper `parseFrequencyDigits`:

```go
// parseFrequencyDigits parses 5 or 6 contiguous digits into av.Frequency.
// 5 digits append an implicit trailing zero ("12775" → 127750).
func parseFrequencyDigits(s string) (av.Frequency, error) {
    if len(s) != 5 && len(s) != 6 {
        return 0, ErrInvalidCommandSyntax
    }
    n, err := strconv.Atoi(s)
    if err != nil {
        return 0, ErrInvalidCommandSyntax
    }
    if len(s) == 5 {
        n *= 10
    }
    if n < 118000 || n > 137000 {
        return 0, ErrInvalidCommandSyntax
    }
    return av.Frequency(n), nil
}
```

- [ ] **Step 11.2: Update `TO` parser**

Replace the `TO` branch at line 4140:

```go
} else if command == "TO" {
    // Bare TO: airport must have exactly one tower.
    return s.ContactTower(tcw, callsign, 0, "", true)
} else if strings.HasPrefix(command, "TO") && len(command) > 2 {
    rest := command[2:]
    var hint string
    if idx := strings.Index(rest, ":"); idx >= 0 {
        hint = rest[idx+1:]
        rest = rest[:idx]
    }
    freq, err := parseFrequencyDigits(rest)
    if err != nil {
        return nil, ErrInvalidCommandSyntax
    }
    return s.ContactTower(tcw, callsign, freq, hint, hint == "")
```

- [ ] **Step 11.3: Build + test**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./... && go test ./sim/ ./aviation/
```

- [ ] **Step 11.4: Commit**

```bash
git add sim/control.go
git commit -m "sim: FC/TO command parsers accept frequency arg + hint suffix"
```

---

### Task 12: Replace STT pattern table

**Files:**
- Modify: `stt/handlers.go:1253-1306`

- [ ] **Step 12.1: Delete old `contact`/`tower` block and re-add per spec**

Replace the entire block from `registerSTTCommand("{garbled_word} tower", ...)` through the closing `registerSTTCommand("frequency change approved", ...)` (lines roughly 1253–1306) with:

```go
// === CONTACT / FREQUENCY CHANGE PATTERNS ===
// See docs/superpowers/specs/2026-04-18-frequency-change-readback-design.md

// TO with explicit frequency and position (highest priority).
registerSTTCommand(
    "contact {text} tower {frequency}",
    func(pos string, f av.Frequency) string {
        return fmt.Sprintf("TO%s:%s", frequencyToDigits(f), pos)
    },
    WithName("contact_position_tower_freq"),
    WithPriority(20),
)
registerSTTCommand(
    "over to {text} tower {frequency}",
    func(pos string, f av.Frequency) string {
        return fmt.Sprintf("TO%s:%s", frequencyToDigits(f), pos)
    },
    WithName("over_to_position_tower_freq"),
    WithPriority(19),
)
// Bare tower with position name (no freq). Requires single-tower airport at runtime.
registerSTTCommand(
    "contact {text} tower",
    func(_ string) string { return "TO" },
    WithName("contact_position_tower_bare"),
    WithPriority(18),
)
registerSTTCommand(
    "contact tower {frequency}",
    func(f av.Frequency) string { return "TO" + frequencyToDigits(f) },
    WithName("contact_tower_freq"),
    WithPriority(16),
)
registerSTTCommand(
    "contact tower",
    func() string { return "TO" },
    WithName("contact_tower_bare"),
    WithPriority(15),
)

// FC with position hint and frequency.
registerSTTCommand(
    "contact {text} {frequency}",
    func(pos string, f av.Frequency) string {
        return fmt.Sprintf("FC%s:%s", frequencyToDigits(f), pos)
    },
    WithName("contact_position_freq"),
    WithPriority(12),
)
registerSTTCommand(
    "over to {text} {frequency}",
    func(pos string, f av.Frequency) string {
        return fmt.Sprintf("FC%s:%s", frequencyToDigits(f), pos)
    },
    WithName("over_to_position_freq"),
    WithPriority(11),
)
registerSTTCommand(
    "contact approach|departure|center|ground|clearance|ramp {frequency}",
    func(f av.Frequency) string { return "FC" + frequencyToDigits(f) },
    WithName("contact_facilitytype_freq"),
    WithPriority(10),
)
registerSTTCommand(
    "contact {frequency}",
    func(f av.Frequency) string { return "FC" + frequencyToDigits(f) },
    WithName("contact_freq"),
    WithPriority(9),
)

registerSTTCommand(
    "frequency change approved",
    func() string { return "" },
    WithName("frequency_change_approved"),
    WithPriority(15),
)
```

Add a helper (same file or a local utility file):

```go
// frequencyToDigits renders a Frequency as a 6-digit string for the command
// string form. 127750 → "127750".
func frequencyToDigits(f av.Frequency) string {
    return fmt.Sprintf("%06d", int(f))
}
```

Ensure `fmt` and `av` imports are present.

- [ ] **Step 12.2: Remove old `contactFrequencyParser`**

Delete the `contactFrequencyParser` type and its `parse`/`identifier`/`goType` methods from `stt/typeparsers.go`. Remove any registration entries for identifier `"contact_frequency"`.

```bash
grep -rn "contact_frequency\|contactFrequencyParser" stt/
```

All references should now be gone.

- [ ] **Step 12.3: Build + run STT tests**

```bash
cd C:/Users/judlo/Documents/vice/vice && go build ./... && go test ./stt/ -v
```

Expected: build passes; any STT tests that relied on old patterns will fail — update them to the new expected commands in-place.

- [ ] **Step 12.4: Commit**

```bash
git add stt/handlers.go stt/typeparsers.go stt/*_test.go
git commit -m "stt: contact/tower grammar preserves frequency + position hint"
```

---

### Task 13: Remove bare `FC` compatibility

**Files:**
- Modify: `sim/control.go` (remove the fall-through for `command == "FC"`)

- [ ] **Step 13.1: Verify no STT path emits bare `FC`**

```bash
grep -n "\"FC\"" stt/
```

All returns should now be `"FC"+digits` form. If any remain, update them.

- [ ] **Step 13.2: Tighten `FC` parser**

In the `case 'F':` block, ensure there is no branch matching bare `command == "FC"`. The only valid form should be `FC{digits}[:hint]`.

- [ ] **Step 13.3: Run full suite**

```bash
cd C:/Users/judlo/Documents/vice/vice && go test ./... 2>&1 | grep -E "FAIL|ok\s"
```

Expected: all packages `ok`.

- [ ] **Step 13.4: Commit**

```bash
git add sim/control.go
git commit -m "sim: remove bare FC fallback; frequency argument now required"
```

---

### Task 14: Integration test + manual verification

**Files:**
- Test: `stt/handlers_test.go` (or existing end-to-end test file)

- [ ] **Step 14.1: Add end-to-end tests**

Add tests that feed representative STT transcripts through the full pipeline and assert the emitted command string:

Before writing the test, identify the existing end-to-end STT entry point by reading how `stt/provider_test.go` drives the pipeline:

```bash
grep -n "func Test" stt/provider_test.go | head
```

Use whatever match/dispatch function it already exercises (likely `MatchCommand` or similar). Then:

```go
func TestSTTContactFrequencyE2E(t *testing.T) {
    cases := []struct {
        in   string
        want string
    }{
        {"contact orlando approach one two seven point seven five", "FC127750:orlando approach"},
        {"contact orlando approach one two seven seven five", "FC127750:orlando approach"},
        {"over to jacksonville center one three four point zero", "FC134000:jacksonville center"},
        {"contact executive tower", "TO"},
        {"contact tower one one eight point three", "TO118300"},
        {"contact approach one two seven point seven five", "FC127750"},
    }
    for _, tc := range cases {
        t.Run(tc.in, func(t *testing.T) {
            // Use the same entry point as existing provider_test.go.
            got := matchSTT(tc.in)
            if got != tc.want {
                t.Errorf("in=%q: got %q, want %q", tc.in, got, tc.want)
            }
        })
    }
}
```

If the existing provider test uses a different convention (e.g. a table-driven helper), extend that helper rather than inventing a new one.

- [ ] **Step 14.2: Manual verification in vice**

Build and launch vice; in a scenario with multi-tower airports (e.g. `ZDC/PCT.json` for IAD_N/E/W_TWR) and single-tower airports (e.g. `ZDC/DCA.json`), verify:

- Typed `UAL123 FC127750` → pilot reads back `"contact Orlando Approach on 127.75"`.
- Typed `UAL123 TO` at DCA → position-only readback `"contact tower"`.
- Typed `UAL123 TO` at IAD → command error (ambiguous tower).
- Typed `UAL123 TO120100` at IAD → full readback `"contact IAD North Tower on 120.1"`.
- Typed `UAL123 FC12345` (bogus freq) → pilot asks `"what was that frequency?"` and aircraft does not switch.
- STT "contact approach one two seven point seven five" → pilot freq-only readback `"127.75"` when same facility.
- STT "over to jacksonville center one three four point zero" → pilot full readback `"contact Jacksonville Center on 134.0"`.

- [ ] **Step 14.3: Commit tests**

```bash
git add stt/handlers_test.go
git commit -m "stt: end-to-end tests for FC/TO contact phrases"
```

---

## Self-review checklist

- [ ] Each spec section (`Commands`, `Tower resolution`, `Frequency resolution`, `Intent data model`, `Typed vs STT`, `STT grammar`, `Readback render`, `Call-site changes`, `Error surface`) maps to at least one task.
- [ ] Types and method signatures are consistent across tasks (`ContactTower(target, freq, positionOnly, lg)` everywhere, not varying).
- [ ] No placeholders: every step has concrete code or command.
- [ ] Each task ends with a commit.
- [ ] Ordering keeps `go build ./...` passing after every commit.

---

## Notes for the implementer

- **Run `go vet ./...` and `gofmt -w .`** after each task. The project commit log shows gofmt/gopls-driven cleanups; keep the branch tidy.
- **Windows build:** use `cmd //c "cd /d C:\Users\judlo\Documents\vice\vice && .\build.bat"` if `build.bat` is needed for the full build (it wraps cgo + static DLLs). `go test ./...` works without it.
- **Warning logging:** `s.lg.Warnf` may be named differently — check `util/log.go` if the method doesn't exist and adapt.
- **`controllerForAircraft`:** the lookup uses `ac.ControllerFrequency` as a `TCP` key. Confirm that matches the `State.Controllers` map key type at the call site; adjust if the map is keyed by sector or position.
- **STT `{text}` match:** the existing `contact {text} tower` pattern already matches position names before "tower". Verify `{text}` behaves the same (single token by default; may need `{text+}` or a multi-token variant — grep `text+\|skip` in `stt/registry.go`).
