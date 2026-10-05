package agent

import (
	"strings"
	"testing"
)

func TestRedactAdversarialCredentials(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"bearer header", "Authorization: Bearer opaque-value", "Authorization: [REDACTED]"},
		{"basic header", "Proxy-Authorization: Basic dXNlcjpwYXNz", "Proxy-Authorization: [REDACTED]"},
		{"quoted JSON", `{"password":"secret with spaces,;}","ok":true}`, `{"password":"[REDACTED]","ok":true}`},
		{"escaped quote", `{"password":"first\"second\\third","token":"another"}`, `{"password":"[REDACTED]","token":"[REDACTED]"}`},
		{"single quotes", `'password': 'first\'second with spaces' status=ok`, `'password': '[REDACTED]' status=ok`},
		{"logfmt", `password="space separated secret" token='other secret'`, `password="[REDACTED]" token='[REDACTED]'`},
		{"query parameters", "/path?access_token=first&refresh_token=second&ok=yes", "/path?access_token=[REDACTED]&refresh_token=[REDACTED]&ok=yes"},
		{"mixed case", "X-API-Key = sensitive; CLIENT_SECRET=other", "X-API-Key = [REDACTED]; CLIENT_SECRET=[REDACTED]"},
		{"tabs", "AUTHORIZATION:\tBearer\topaque\tstatus=ok", "AUTHORIZATION:\t[REDACTED]\tstatus=ok"},
		{"multiline", "password=first\ntoken=second\nstatus=ok", "password=[REDACTED]\ntoken=[REDACTED]\nstatus=ok"},
		{"unterminated quote", `password="first second`, `password="[REDACTED]`},
		{"standalone bearer", "sent bearer opaque+/==", "sent Bearer [REDACTED]"},
		{"standalone JWT", "sent eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature", "sent [REDACTED_JWT]"},
		{"array boundary", `[password=first,token=second]`, `[password=[REDACTED],token=[REDACTED]]`},
		{"empty values", `{"password":"","token":null}`, `{"password":"[REDACTED]","token":[REDACTED]}`},
		{"unrelated keys", "token_count=5 notpassword=visible secret_name=visible authorization_status=ok", "token_count=5 notpassword=visible secret_name=visible authorization_status=ok"},
		{"ordinary diagnostics", "ERROR upstream timeout after 30s endpoint=/health", "ERROR upstream timeout after 30s endpoint=/health"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Redact(tt.input)
			if got != tt.want {
				t.Fatalf("Redact() = %q; want %q", got, tt.want)
			}
			if again := Redact(got); again != got {
				t.Fatalf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestWriteErrorRedactsAllFormats(t *testing.T) {
	for _, format := range []string{"json", "ndjson", "text"} {
		t.Run(format, func(t *testing.T) {
			var output strings.Builder
			err := WriteError(&output, "failed", `Authorization: Bearer sentinel-one password="sentinel-two with spaces"`, format)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "sentinel-") || !strings.Contains(output.String(), "[REDACTED]") {
				t.Fatalf("unsafe output: %s", output.String())
			}
		})
	}
}

func FuzzRedactQuotedCredential(f *testing.F) {
	for _, seed := range []string{"spaces and punctuation,;}", "quote\"slash\\", "\nnext line", "", "日本語"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, secret string) {
		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(secret)
		input := "password=\"" + escaped + "\" status=ok"
		if got := Redact(input); got != `password="[REDACTED]" status=ok` {
			t.Fatalf("Redact() = %q", got)
		}
	})
}
