package web

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeWebErrorMessageRedactsBoundsAndRemovesControls(t *testing.T) {
	payload := "password=downstream-secret\r\n<script>alert(1)</script>\u202e" + strings.Repeat("x", maxWebErrorMessageRunes+100)
	got := safeWebErrorMessage(payload)
	if strings.Contains(got, "downstream-secret") || !strings.Contains(got, "password=[REDACTED]") {
		t.Fatalf("downstream secret was not redacted: %q", got)
	}
	if strings.ContainsAny(got, "\r\n") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("control or formatting character survived: %q", got)
	}
	if !utf8.ValidString(got) || len([]rune(got)) > maxWebErrorMessageRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("error boundary was not valid and bounded: runes=%d suffix=%q", len([]rune(got)), got[len(got)-3:])
	}
}

func TestTrustedTemplateTypesStayInsideReviewedMFAQREncoder(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	trustedHTML := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(set, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if !ok || owner.Name != "template" {
				return true
			}
			dangerous := map[string]bool{"HTML": true, "JS": true, "URL": true, "CSS": true, "HTMLAttr": true}
			if !dangerous[selector.Sel.Name] {
				return true
			}
			if selector.Sel.Name != "HTML" || filepath.Base(name) != "mfa_web.go" {
				t.Errorf("unreviewed trusted template type %s.%s in %s", owner.Name, selector.Sel.Name, name)
				return true
			}
			trustedHTML++
			return true
		})
	}
	if trustedHTML != 3 {
		t.Fatalf("reviewed template.HTML surface = %d, want 3 MFA QR occurrences", trustedHTML)
	}
}

func TestFirstPartyDOMSinkInventoryAndPurifierFailClosed(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("ui", "assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{".outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "main.childNodes.forEach"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unreviewed first-party DOM API %q", forbidden)
		}
	}
	if got := strings.Count(text, ".innerHTML"); got != 32 {
		t.Fatalf("reviewed innerHTML inventory changed: got %d, want 32; review escaping and update this gate", got)
	}
	if !strings.Contains(text, `if (!purify?.sanitize) throw new Error(words().loadFailed)`) ||
		!strings.Contains(text, `purify.sanitize(main.innerHTML, { RETURN_DOM_FRAGMENT: true })`) {
		t.Fatal("same-origin document import no longer fails closed through DOMPurify")
	}
}

func TestMFAQRSVGNeverInterpolatesEnrollmentURI(t *testing.T) {
	payload := `otpauth://totp/ScriptBoard:%3C/script%3E%3Csvg%20onload=alert(1)%3E?secret=AAAAAAAAAAAAAAAA`
	markup, err := renderMFAEnrollmentQRCode(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(markup)
	for _, forbidden := range []string{"otpauth", "script", "onload", "javascript:", "data:"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("enrollment input reached trusted SVG as %q", forbidden)
		}
	}
	if !strings.HasPrefix(text, `<svg data-mfa-qr`) || !strings.HasSuffix(text, `</svg>`) {
		t.Fatalf("unexpected trusted QR markup: %q", text)
	}
}
