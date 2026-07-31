//go:build windows

package runmanager

import "testing"

func TestDecodeWindowsCodePage(t *testing.T) {
	raw := []byte{
		0xca, 0xe4, 0xb3, 0xf6, 0xc8, 0xd5, 0xd6, 0xbe, 0xa3,
		0xba, 0xd6, 0xd0, 0xce, 0xc4, 0xd5, 0xfd, 0xb3, 0xa3,
	}
	const expected = "输出日志：中文正常"
	decoded, ok := decodeWindowsCodePage(raw, 936)
	if !ok || decoded != expected {
		t.Fatalf("decoded CP936 output = %q, ok=%v; want %q", decoded, ok, expected)
	}
}
