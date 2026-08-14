//go:build windows

package windowsidentity

import "testing"

func TestResolveSIDDerivesManagedServiceSIDsWithoutAccountLookup(t *testing.T) {
	for identity, expected := range map[string]string{
		`NT SERVICE\ScriptBoard`:       "S-1-5-80-3355417997-3674384075-3770669436-3273107343-2989225967",
		`NT SERVICE\ScriptBoardRunner`: "S-1-5-80-692106500-646567557-1573084673-140932486-2344078395",
	} {
		sid, err := ResolveSID(identity)
		if err != nil || sid.String() != expected {
			t.Fatalf("ResolveSID(%q)=%v, %v; want %s", identity, sid, err, expected)
		}
	}
}
