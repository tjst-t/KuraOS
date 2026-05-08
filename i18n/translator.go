package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localesFS embed.FS

const defaultLocale = "ja"

type Translator struct {
	mu       sync.RWMutex
	locale   string
	messages map[MessageID]string
}

// New returns a Translator preloaded with the default locale (ja).
// The locale is embedded at build time via embed.FS so the binary stays
// self-contained — no runtime locale file dependency.
func New() (*Translator, error) {
	t := &Translator{}
	if err := t.Load(defaultLocale); err != nil {
		return nil, err
	}
	return t, nil
}

// Load swaps the active locale. Unknown locale -> error so callers see the
// failure instead of silently falling back (DESIGN_PRINCIPLES: 明示的 > 暗黙的).
func (t *Translator) Load(locale string) error {
	path := fmt.Sprintf("locales/%s.json", locale)
	raw, err := fs.ReadFile(localesFS, path)
	if err != nil {
		return fmt.Errorf("i18n: read locale %q: %w", locale, err)
	}
	var msgs map[string]string
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return fmt.Errorf("i18n: parse locale %q: %w", locale, err)
	}
	typed := make(map[MessageID]string, len(msgs))
	for k, v := range msgs {
		typed[MessageID(k)] = v
	}
	t.mu.Lock()
	t.locale = locale
	t.messages = typed
	t.mu.Unlock()
	return nil
}

// T renders the message for id, substituting positional placeholders {0}, {1}, ...
// with the corresponding args. A missing id returns the id itself so the gap is
// visible in production rather than silently falling back to a generic string.
func (t *Translator) T(id MessageID, args ...any) string {
	t.mu.RLock()
	msg, ok := t.messages[id]
	t.mu.RUnlock()
	if !ok {
		return string(id)
	}
	if len(args) == 0 {
		return msg
	}
	for i, a := range args {
		msg = strings.ReplaceAll(msg, fmt.Sprintf("{%d}", i), fmt.Sprint(a))
	}
	return msg
}

// Locale returns the currently active locale code (e.g. "ja").
func (t *Translator) Locale() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.locale
}

// Has reports whether id is known in the active locale. Used by tests to assert
// the embedded JSON contains all MessageID constants this binary may emit.
func (t *Translator) Has(id MessageID) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.messages[id]
	return ok
}
