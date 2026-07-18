# Subscription Settings Modal Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace inline subscription cycling and the appended model-map panel with a responsive overlay that lists, selects, configures, adds, renames, and deletes every subscription profile.

**Architecture:** Add provider-aware profile initialization to `internal/claudeconfig`, then introduce a cohesive subscription-modal state and renderer in `internal/tui`. The modal reuses the existing full-screen overlay compositor, keeps edits in a profile-local draft until an explicit save, and routes every mutation through the existing persistence and OpenCode-sync boundaries.

**Tech Stack:** Go, Bubble Tea, Bubbles text input, Lip Gloss, table-driven Go tests, existing `claudeconfig` JSON/list/pointer storage.

**Repository constraint:** Work directly on the existing `main` branch. Do not create a branch or worktree.

---

### Task 1: Add provider presentation metadata and initialized profile creation

**Files:**
- Modify: `internal/claudeconfig/catalog.go`
- Modify: `internal/claudeconfig/claudeconfig.go`
- Modify: `internal/claudeconfig/claudeconfig_test.go`

**Step 1: Write failing catalog tests**

Add tests that require every provider to expose a friendly name and four valid
default alias mappings:

```go
func TestProviders_haveDisplayNamesAndValidDefaults(t *testing.T) {
	for _, provider := range Providers {
		if provider.Name == "" {
			t.Errorf("provider %q has no display name", provider.Key)
		}
		models := map[string]bool{}
		for _, model := range provider.Models {
			models[model.ID] = true
		}
		for i, id := range provider.DefaultModels {
			if id == "" || !models[id] {
				t.Errorf("provider %q default %s = %q, want catalog model",
					provider.Key, AnthropicAliases[i], id)
			}
		}
	}
}

func TestProviderByKey_returnsCatalogProvider(t *testing.T) {
	got, ok := ProviderByKey("openai-chatgpt")
	if !ok || got.Name != "OpenAI / ChatGPT" {
		t.Fatalf("ProviderByKey = (%+v, %v)", got, ok)
	}
}
```

**Step 2: Run the catalog tests and verify RED**

Run:

```bash
go test ./internal/claudeconfig -run 'TestProviders_haveDisplayNamesAndValidDefaults|TestProviderByKey_returnsCatalogProvider' -count=1 -v
```

Expected: compile failure because `Provider.Name`, `Provider.DefaultModels`, and
`ProviderByKey` do not exist.

**Step 3: Add provider metadata**

Extend `Provider`:

```go
type Provider struct {
	Key            string
	Name           string
	Aliases        []string
	BaseURL        string
	Models         []Model
	DefaultModels  [4]string
	Auth           AuthKind
	MirrorOpenCode bool
}
```

Populate:

```go
// Zhipu / GLM
Name: "Zhipu / GLM",
DefaultModels: [4]string{"glm-4.7", "glm-4.7", "glm-4.5-air", "glm-4.5-air"},

// Xiaomi MiMo
Name: "Xiaomi MiMo",
DefaultModels: [4]string{"mimo-v2.5-pro", "mimo-v2.5-pro", "mimo-v2.5", "mimo-v2.5"},

// OpenAI / ChatGPT
Name: "OpenAI / ChatGPT",
DefaultModels: [4]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-luna"},
```

Export the existing lookup without duplicating catalog traversal:

```go
func ProviderByKey(key string) (Provider, bool) {
	return providerByKey(key)
}
```

**Step 4: Run the catalog tests and verify GREEN**

Run the Step 2 command.

Expected: PASS.

**Step 5: Write failing provider-aware add tests**

Add table-driven coverage:

```go
func TestAddForProvider_writesInitializedProfile(t *testing.T) {
	for _, key := range []string{"zhipu", "mimo", "openai-chatgpt"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			list := filepath.Join(dir, "claude-configs.list")
			configsDir := filepath.Join(dir, "claude-configs")

			file, err := AddForProvider(list, configsDir, "My profile", key)
			if err != nil {
				t.Fatal(err)
			}
			provider, _ := ProviderByKey(key)
			cfg := Config{Name: "My profile", File: file}
			if got := ProviderForConfig(configsDir, cfg).Key; got != key {
				t.Errorf("provider = %q, want %q", got, key)
			}
			got := ReadModelMappings(configsDir, file, ProviderModels[key])
			for i, modelID := range provider.DefaultModels {
				want := slices.Index(ProviderModels[key], modelID)
				if got[i] != want {
					t.Errorf("mapping[%d] = %d, want %d", i, got[i], want)
				}
			}
			if provider.Auth == AuthAPIKey && ConfigReady(configsDir, cfg) {
				t.Error("new API-key profile must need a key")
			}
		})
	}
}

func TestAddForProvider_rejectsUnknownProviderWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	_, err := AddForProvider(filepath.Join(dir, "list"), filepath.Join(dir, "cfg"), "Bad", "missing")
	if err == nil {
		t.Fatal("unknown provider accepted")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("unknown provider left artifacts: %v", entries)
	}
}
```

Also assert the config file mode is `0600`, the explicit provider marker is
present, API providers receive `ANTHROPIC_BASE_URL`, and ChatGPT receives the
top-level default `model`.

**Step 6: Run the add tests and verify RED**

Run:

```bash
go test ./internal/claudeconfig -run '^TestAddForProvider_' -count=1 -v
```

Expected: compile failure because `AddForProvider` does not exist.

**Step 7: Implement provider-aware creation**

Add:

```go
func AddForProvider(listFile, configsDir, name, providerKey string) (string, error)
```

Implementation requirements:

1. Resolve the provider before creating directories or files.
2. Reuse a private `nextConfigFilename(configsDir, name)` helper shared with
   `Add`.
3. Build JSON with `$schema`, an `env` object, the explicit
   `WISP_DECK_SUBSCRIPTION_PROVIDER`, provider endpoint when non-empty, and all
   four default model environment variables.
4. Set top-level `model` to the Sonnet default for ChatGPT.
5. Use `writeSecure` for the config file.
6. Append the list entry only after the config exists.
7. Remove the config file if appending the list entry fails.

Keep legacy `Add` behavior intact for its CLI compatibility tests.

**Step 8: Run the package and verify GREEN**

Run:

```bash
gofmt -w internal/claudeconfig/catalog.go internal/claudeconfig/claudeconfig.go internal/claudeconfig/claudeconfig_test.go
go test ./internal/claudeconfig -count=1
```

Expected: PASS.

**Step 9: Commit**

```bash
git add internal/claudeconfig/catalog.go internal/claudeconfig/claudeconfig.go internal/claudeconfig/claudeconfig_test.go
git commit -m "feat(config): initialize provider profiles"
```

---

### Task 2: Add modal state and route subscription entry points

**Files:**
- Create: `internal/tui/subscription_modal.go`
- Create: `internal/tui/subscription_modal_test.go`
- Modify: `internal/tui/mainmenu.go`
- Modify: `internal/tui/render_settings.go`
- Modify: `internal/tui/mouse.go`
- Modify: `internal/tui/mainmenu_subscription_focus_test.go`
- Modify: `internal/tui/claude_config_panel_test.go`

**Step 1: Write failing open/close and entry-point tests**

Create a fixture with Standard plus Zhipu, MiMo, and ChatGPT profiles. Add tests:

```go
func TestSubscriptionModal_openFocusesActiveProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.SetActiveClaudeConfig("openai-gpt.json")
	m.openSubscriptionModal()
	if !m.subscriptionModal.open {
		t.Fatal("modal did not open")
	}
	if got := m.subscriptionModalProfile().File; got != "openai-gpt.json" {
		t.Fatalf("focused file = %q", got)
	}
}

func TestSettingsEnter_onSubscriptionOpensModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.settingsSelected = rowSubscription
	_, _ = m.settingsEnter()
	if !m.subscriptionModal.open {
		t.Fatal("Subscription Enter must open the modal")
	}
}

func TestSettingsArrows_doNotChangeSubscription(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.settingsSelected = rowSubscription
	before := m.CurrentClaudeConfigFile()
	m.settingsValueRight()
	m.settingsValueLeft()
	if got := m.CurrentClaudeConfigFile(); got != before {
		t.Fatalf("settings arrows changed profile to %q", got)
	}
}

func TestPlanEnter_opensSubscriptionModal(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.focus = FocusSubscription
	_, _ = m.focusEnter()
	if !m.subscriptionModal.open {
		t.Fatal("PLAN Enter must open the modal")
	}
}
```

Add Esc and Ctrl+C tests proving they close only the modal, not Wisp Deck.

**Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/tui -run 'TestSubscriptionModal_|TestSettingsEnter_onSubscription|TestSettingsArrows_doNotChangeSubscription|TestPlanEnter_opensSubscriptionModal' -count=1 -v
```

Expected: compile failure because modal state and methods do not exist.

**Step 3: Define cohesive modal state**

In `subscription_modal.go` define:

```go
type subscriptionPane int

const (
	subscriptionProfilesPane subscriptionPane = iota
	subscriptionDetailsPane
)

type subscriptionModalMode int

const (
	subscriptionBrowse subscriptionModalMode = iota
	subscriptionEditKey
	subscriptionAddProvider
	subscriptionAddName
	subscriptionRename
	subscriptionDeleteConfirm
	subscriptionDiscardConfirm
)

type subscriptionDraft struct {
	file      string
	models    []string
	mappings  [4]int
	apiKey    string
	keyEdited bool
	dirty     bool
}

type subscriptionModalState struct {
	open          bool
	pane          subscriptionPane
	mode          subscriptionModalMode
	profileCursor int
	detailCursor  int
	profileOffset int
	detailOffset  int
	hover         subscriptionHitTarget
	draft         subscriptionDraft
	input         textinput.Model
	providerKey   string
	err           error
}
```

Add the state as one field on `MainMenuModel`.

Define a UI profile projection:

```go
type subscriptionProfile struct {
	Name     string
	File     string
	Provider claudeconfig.Provider
	Standard bool
	Active   bool
	Ready    bool
}
```

`subscriptionProfiles()` returns virtual Standard followed by
`m.claudeConfigs`. `openSubscriptionModal()` focuses the active file and loads
its draft. `updateSubscriptionModal()` initially supports Esc/Ctrl+C, profile
up/down, and Tab pane switching.

**Step 4: Route key and mouse entry points**

- Intercept `subscriptionModal.open` before every other modal in `Update`.
- Make Settings Enter and click call `openSubscriptionModal()`.
- Make PLAN Enter call `openSubscriptionModal()`.
- Remove `rowSubscription` from `settingsValueLeft/Right`.
- Keep PLAN left/right quick cycling unchanged.
- Change Settings help copy for Subscription to `⏎ manage`.

Do not remove the legacy model-map implementation yet; stop routing new input
to it.

**Step 5: Run focused tests and verify GREEN**

Run the Step 2 command plus:

```bash
go test ./internal/tui -run 'Subscription|SettingsHelpRow|FocusSubscription' -count=1
```

Expected: PASS after updating obsolete assertions that expected Settings arrows
or model-map entry.

**Step 6: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_test.go internal/tui/mainmenu.go internal/tui/render_settings.go internal/tui/mouse.go internal/tui/mainmenu_subscription_focus_test.go internal/tui/claude_config_panel_test.go
git commit -m "feat(tui): open subscription management modal"
```

---

### Task 3: Render the responsive overlay and provider details

**Files:**
- Modify: `internal/tui/subscription_modal.go`
- Create: `internal/tui/subscription_modal_render_test.go`
- Modify: `internal/tui/mainmenu.go`
- Modify: `internal/tui/overlay_card_test.go`

**Step 1: Write failing wide-overlay render tests**

Test at `100x36`:

```go
func TestSubscriptionModal_wideRenderShowsInventoryAndDetails(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.width, m.height = 100, 36
	m.openSubscriptionModal()
	view := stripAnsi(m.View())
	for _, want := range []string{
		"Subscriptions", "PROFILES", "Standard Claude",
		"Zhipu GLM", "Xiaomi MiMo", "OpenAI GPT",
		"Provider", "Authentication", "MODEL ROUTING",
		"Opus", "Sonnet", "Haiku", "Fable",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestSubscriptionModal_overlayDimsSettingsBackdrop(t *testing.T) {
	// Assert the output keeps terminal dimensions, contains SGR faintness, and
	// places the card away from (0,0).
}

func TestSubscriptionModal_activeAndCursorMarkersDiffer(t *testing.T) {
	// Persist OpenAI as active, move cursor to MiMo, and assert both active and
	// cursor glyphs remain visible on different rows.
}
```

**Step 2: Write failing compact and height tests**

At width 60, assert only `PROFILES` is shown initially, Enter/Right switches to
details, and details fit the same card width. At a short height, assert the
header/footer stay visible and moving the cursor adjusts the relevant offset.

**Step 3: Run render tests and verify RED**

Run:

```bash
go test ./internal/tui -run '^TestSubscriptionModal_.*Render|^TestSubscriptionModal_overlay|^TestSubscriptionModal_active|^TestSubscriptionModal_compact|^TestSubscriptionModal_short' -count=1 -v
```

Expected: failures because no card renderer or overlay integration exists.

**Step 4: Implement shared geometry**

Add:

```go
const (
	subscriptionModalMaxWidth = 92
	subscriptionModalMinWide  = 64
	subscriptionListWidth     = 28
)

func (m *MainMenuModel) subscriptionModalLayout() (left, top, width, height int)
func (m *MainMenuModel) subscriptionModalCompact() bool
```

Clamp width and height to terminal minus a two-cell margin. Use one geometry
function for render and hit testing.

**Step 5: Implement the card renderer**

Add:

```go
func (m *MainMenuModel) renderSubscriptionModalCard() string
func (m *MainMenuModel) renderSubscriptionProfiles(width, bodyHeight int) []string
func (m *MainMenuModel) renderSubscriptionDetails(width, bodyHeight int) []string
func (m *MainMenuModel) overlaySubscriptionModal(placed string) string
```

Requirements:

- Use rounded gray border and an embedded `Subscriptions` title.
- Use the current theme accent only for focused cursor/action.
- Use green `Ready`, amber `Needs key`, and red only for errors/delete.
- Render active `●` separately from focus `▌`.
- Use provider friendly names from the catalog.
- Standard details explain native Claude Code login/model selection.
- API providers show masked key readiness and base URL.
- ChatGPT shows `codex login` and `Local Codex bridge`.
- Align model alias/value columns.
- Keep footer fixed while each body pane scrolls.

In `View`, render the Settings box normally, place it, then overlay the modal
after Browser/About checks at the appropriate modal priority.

**Step 6: Verify GREEN and widths**

Run:

```bash
gofmt -w internal/tui/subscription_modal.go internal/tui/subscription_modal_render_test.go internal/tui/mainmenu.go internal/tui/overlay_card_test.go
go test ./internal/tui -run 'TestSubscriptionModal_|TestOverlayCard_' -count=1
```

Expected: PASS. Every stripped card line must have the computed visible width.

**Step 7: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_render_test.go internal/tui/mainmenu.go internal/tui/overlay_card_test.go
git commit -m "feat(tui): render subscription settings overlay"
```

---

### Task 4: Implement draft editing, explicit save, and profile activation

**Files:**
- Modify: `internal/tui/subscription_modal.go`
- Modify: `internal/tui/subscription_modal_test.go`
- Modify: `internal/tui/subscription_modal_render_test.go`
- Modify: `internal/tui/mainmenu.go`

**Step 1: Write failing preview and activation tests**

```go
func TestSubscriptionModal_previewDoesNotPersistActiveProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	m.subscriptionModal.profileCursor = 2
	if got := claudeconfig.GetActive(m.claudeConfigFile); got != "" {
		t.Fatalf("preview persisted %q", got)
	}
}

func TestSubscriptionModal_useProfilePersistsAndSyncs(t *testing.T) {
	// Focus a ready custom profile, trigger 'u', assert selectedConfig, pointer,
	// and injected OpenCode sync hook each update exactly once.
}

func TestSubscriptionModal_refusesUnreadyProfile(t *testing.T) {
	// Focus an API-key profile without a key; Use leaves pointer unchanged and
	// renders an inline error.
}
```

**Step 2: Run and verify RED**

Run:

```bash
go test ./internal/tui -run '^TestSubscriptionModal_(preview|useProfile|refusesUnready)' -count=1 -v
```

Expected: failures because `useSubscriptionProfile` is absent.

**Step 3: Implement explicit activation**

Add `useSubscriptionProfile()`:

- Standard calls `claudeconfig.SetActive(pointer, "")`.
- Custom profiles must pass `ConfigReady`.
- Update `selectedConfig` only after persistence succeeds.
- Call the existing OpenCode sync hook after success.
- Keep the modal open and refresh active badges.
- Report errors inline.

**Step 4: Write failing mapping/key draft tests**

Cover:

- loading a profile creates a clean draft;
- left/right changes only the draft;
- changing profiles with a dirty draft opens discard confirmation;
- canceling discard preserves the draft and focused profile;
- confirming discard loads the next profile;
- `s`/Save writes mappings and an edited API key;
- failed writes preserve the dirty draft and modal; and
- ChatGPT never exposes key-edit mode.

**Step 5: Run and verify RED**

Run:

```bash
go test ./internal/tui -run '^TestSubscriptionModal_(draft|dirty|discard|save|chatGPT)' -count=1 -v
```

Expected: failures because draft navigation/save is incomplete.

**Step 6: Implement detail navigation and save**

Define stable detail rows:

```go
const (
	subscriptionDetailOpus = iota
	subscriptionDetailSonnet
	subscriptionDetailHaiku
	subscriptionDetailFable
	subscriptionDetailAuth
	subscriptionDetailSave
)
```

Implement:

- `loadSubscriptionDraft(profile)`;
- `cycleSubscriptionMapping(direction)`;
- masked `textinput` API-key edit mode;
- `saveSubscriptionDraft()` writing mappings first and the key second;
- dirty-state discard confirmation before profile switch/close; and
- provider-specific help text.

If key persistence fails after mappings succeed, reload mappings from disk and
keep an accurate dirty draft rather than claiming the whole save failed
atomically.

**Step 7: Run focused and regression tests**

```bash
gofmt -w internal/tui/subscription_modal.go internal/tui/subscription_modal_test.go internal/tui/subscription_modal_render_test.go internal/tui/mainmenu.go
go test ./internal/tui -run 'TestSubscriptionModal_|TestModelMap_|TestClaudeConfig_' -count=1
```

Expected: PASS.

**Step 8: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_test.go internal/tui/subscription_modal_render_test.go internal/tui/mainmenu.go
git commit -m "feat(tui): edit and activate subscription profiles"
```

---

### Task 5: Add profile lifecycle flows inside the modal

**Files:**
- Modify: `internal/tui/subscription_modal.go`
- Create: `internal/tui/subscription_modal_lifecycle_test.go`
- Modify: `internal/tui/subscription_modal_render_test.go`

**Step 1: Write failing add-flow tests**

Cover:

- `a` shows all three provider choices;
- choosing a provider then entering a name calls `AddForProvider`;
- the new profile appears and receives focus;
- it remains inactive;
- an empty name stays in input mode; and
- persistence errors remain inline with no phantom row.

Example:

```go
func TestSubscriptionModal_addProviderProfile(t *testing.T) {
	m := newSubscriptionModalMenu(t)
	m.openSubscriptionModal()
	keySubscriptionModal(t, m, "a")
	selectSubscriptionProvider(t, m, "openai-chatgpt")
	submitSubscriptionName(t, m, "Research GPT")

	cfg := findConfig(m.claudeConfigs, "Research GPT")
	if cfg.File == "" {
		t.Fatal("new profile missing from inventory")
	}
	if got := claudeconfig.ProviderForConfig(m.claudeConfigsDir,
		claudeconfig.Config{Name: cfg.Name, File: cfg.File}).Key; got != "openai-chatgpt" {
		t.Fatalf("provider = %q", got)
	}
	if m.CurrentClaudeConfigFile() == cfg.File {
		t.Fatal("new profile became active without Use")
	}
}
```

**Step 2: Run add tests and verify RED**

```bash
go test ./internal/tui -run '^TestSubscriptionModal_add' -count=1 -v
```

Expected: failures because add modes are not implemented.

**Step 3: Implement add provider/name modes**

- Render provider catalog rows in the detail pane.
- Use up/down plus Enter for provider choice.
- Initialize a text input with a provider-derived default name.
- Call `claudeconfig.AddForProvider`.
- Reload `m.claudeConfigs` from the list file.
- Focus the new file and load its clean draft.
- Call OpenCode sync only after successful creation.

**Step 4: Write failing rename/delete tests**

Cover:

- Standard ignores rename/delete;
- rename updates list display name and preserves provider marker;
- delete requires explicit Enter confirmation;
- deleting inactive profile keeps active pointer;
- deleting active profile selects Standard and syncs;
- errors keep the profile inventory unchanged; and
- Esc cancels each submode.

**Step 5: Run lifecycle tests and verify RED**

```bash
go test ./internal/tui -run '^TestSubscriptionModal_(rename|delete|standard)' -count=1 -v
```

Expected: failures because lifecycle modes are incomplete.

**Step 6: Implement rename and guarded delete**

Use `claudeconfig.Rename` and `claudeconfig.Delete`; reload from disk only after
success. Standard has no lifecycle actions. Deleting the focused row chooses
the nearest surviving cursor, reloads its draft, updates `selectedConfig` from
the pointer, and syncs OpenCode.

**Step 7: Run package tests**

```bash
gofmt -w internal/tui/subscription_modal.go internal/tui/subscription_modal_lifecycle_test.go internal/tui/subscription_modal_render_test.go
go test ./internal/tui ./internal/claudeconfig -count=1
```

Expected: PASS.

**Step 8: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_lifecycle_test.go internal/tui/subscription_modal_render_test.go
git commit -m "feat(tui): manage subscription profile lifecycle"
```

---

### Task 6: Add pointer parity and remove the appended model-map UI

**Files:**
- Modify: `internal/tui/subscription_modal.go`
- Create: `internal/tui/subscription_modal_mouse_test.go`
- Modify: `internal/tui/mouse.go`
- Modify: `internal/tui/mainmenu.go`
- Modify: `internal/tui/claude_config_panel.go`
- Modify: `internal/tui/claude_config_panel_test.go`
- Modify: `internal/tui/mouse_test.go`

**Step 1: Write failing hit-test tests**

Cover geometry-derived targets for:

- profile rows;
- add row;
- four mapping rows;
- API-key row;
- Use, Save, Rename, and Delete buttons;
- outside-card click; and
- compact list/detail drill-in.

Assert hover never moves keyboard cursors and whitespace between controls is not
clickable.

**Step 2: Run and verify RED**

```bash
go test ./internal/tui -run '^TestSubscriptionModalMouse_|^TestSubscriptionModalHit' -count=1 -v
```

Expected: compile/test failures because modal mouse routing is absent.

**Step 3: Implement modal mouse ownership**

- Intercept all mouse messages while the modal is open.
- Derive hit targets from `subscriptionModalLayout`.
- Reuse `frameCellHasGlyph` for bounded targets.
- Motion updates transient hover only.
- Wheel scrolls or moves the active pane.
- Left press mirrors keyboard actions.
- Outside left press closes only a clean browse modal; dirty/submode state
  remains protected.

**Step 4: Verify mouse GREEN**

Run the Step 2 command.

Expected: PASS.

**Step 5: Remove obsolete appended panel state and routes**

Delete:

- `modelMapOpen`, cursor/hover/key-mode fields from `MainMenuModel`;
- appended `renderModelMapPanel` wiring;
- model-map-specific mouse origin/target logic; and
- tests that assert appended-panel coordinates.

Retain provider-independent helpers such as `configAPIKeyIndicator` if still
used by the Settings row. Move any retained helpers to the modal or a small
config presentation file.

Replace legacy behavioral tests with modal equivalents before deleting them.

**Step 6: Run broad TUI regression tests**

```bash
gofmt -w internal/tui
go test ./internal/tui ./test/internal/tui ./cmd/wisp-deck-tui -count=1 -timeout=5m
```

Expected: PASS.

**Step 7: Commit**

```bash
git add internal/tui/subscription_modal.go internal/tui/subscription_modal_mouse_test.go internal/tui/mouse.go internal/tui/mainmenu.go internal/tui/claude_config_panel.go internal/tui/claude_config_panel_test.go internal/tui/mouse_test.go
git commit -m "refactor(tui): retire appended subscription panel"
```

---

### Task 7: Document, verify, install, and publish

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Test: all affected packages and repository suite

**Step 1: Write a failing documentation assertion**

Extend `test/bash/chatgpt_subscription_docs_test.go` or add a focused static
test requiring README copy for:

- Settings → Subscription;
- the overlay's profile inventory;
- `Use profile` and `Save changes`; and
- add/rename/delete profile management.

**Step 2: Run and verify RED**

```bash
go test ./test/bash -run '^TestSubscriptionModalDocumentation' -count=1 -v
```

Expected: FAIL because README does not describe the modal.

**Step 3: Update user documentation**

Document the modal controls and provider-specific authentication. Add an
Unreleased changelog entry describing the new subscription-management overlay.

**Step 4: Run focused verification**

```bash
gofmt -w internal/claudeconfig internal/tui
go test ./internal/claudeconfig ./internal/tui ./test/internal/tui ./cmd/wisp-deck-tui -count=1 -timeout=5m
go test -race ./internal/claudeconfig ./internal/tui -count=1 -timeout=5m
go vet ./...
git diff --check
```

Expected: every command exits 0.

**Step 5: Run the complete repository harness**

The Bash package is documented as requiring up to 20 minutes on loaded
machines. Keep package scheduling serial so its load does not distort unrelated
tight timing assertions:

```bash
./run-tests.sh -p=1 -timeout=20m -count=1
```

Expected: PASS for every package.

**Step 6: Synchronize main**

```bash
git pull --rebase
```

If the rebase changes source, rerun Steps 4 and 5. Resolve conflicts without
discarding unrelated user work. Never force-push.

**Step 7: Install locally**

```bash
make install
```

Verify:

```bash
resolved="$(command -v wisp-deck-tui)"
test "$resolved" = "$HOME/.local/bin/wisp-deck-tui"
test "$(shasum -a 256 bin/wisp-deck-tui | awk '{print $1}')" = \
     "$(shasum -a 256 "$resolved" | awk '{print $1}')"
codesign --verify --verbose=2 bin/wisp-deck-tui
codesign --verify --verbose=2 "$resolved"
```

**Step 8: Commit docs, push, and verify repository state**

```bash
git add README.md CHANGELOG.md test/bash/chatgpt_subscription_docs_test.go
git commit -m "docs: explain subscription settings modal"
git push origin main
git status --short --branch
test "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)"
```

Expected: clean `main...origin/main`.

**Step 9: Handoff**

Report:

- the overlay interaction and lifecycle actions;
- provider-specific authentication behavior;
- focused, race, vet, and full-suite evidence;
- installed path, matching SHA-256, and signature verification;
- pushed commit; and
- that running Wisp Deck ledger panes/sessions must be relaunched.
