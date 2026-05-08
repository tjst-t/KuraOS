package i18n

import (
	"testing"
)

func TestNew_LoadsDefaultLocale(t *testing.T) {
	tr, err := New()
	if err != nil {
		// [AC-S0ff37f-3-1] embedded ja.json must load successfully
		t.Fatalf("New() error: %v", err)
	}
	if got := tr.Locale(); got != "ja" {
		t.Fatalf("Locale() = %q, want %q", got, "ja")
	}
}

func TestT_RendersKnownMessages(t *testing.T) {
	tr, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	cases := []struct {
		name string
		id   MessageID
		args []any
		want string
	}{
		{
			name: "healthy_no_args",
			id:   MsgSystemHealthy,
			args: nil,
			want: "正常稼働中",
		},
		{
			name: "startup_with_port",
			id:   MsgSystemStartup,
			args: []any{8080},
			want: "kura を起動しています (port 8080)",
		},
		{
			name: "shutdown_no_args",
			id:   MsgSystemShutdown,
			args: nil,
			want: "kura を停止しています",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tr.T(c.id, c.args...); got != c.want {
				t.Fatalf("T(%q) = %q, want %q", c.id, got, c.want)
			}
		})
	}
}

func TestT_UnknownIDReturnsID(t *testing.T) {
	tr, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	const unknown MessageID = "no.such.key"
	if got := tr.T(unknown); got != string(unknown) {
		t.Fatalf("T(unknown) = %q, want %q", got, string(unknown))
	}
}

func TestHas_DeclaredMessageIDsAllHaveTranslations(t *testing.T) {
	tr, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	declared := []MessageID{MsgSystemStartup, MsgSystemHealthy, MsgSystemShutdown}
	for _, id := range declared {
		t.Run(string(id), func(t *testing.T) {
			if !tr.Has(id) {
				t.Fatalf("locale ja missing translation for %q", id)
			}
		})
	}
}

func TestLoad_UnknownLocaleErrors(t *testing.T) {
	tr, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := tr.Load("xx"); err == nil {
		t.Fatalf("Load(xx) error = nil, want error")
	}
	// active locale must remain ja after failed Load
	if got := tr.Locale(); got != "ja" {
		t.Fatalf("Locale() after failed Load = %q, want %q", got, "ja")
	}
}
