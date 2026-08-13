//go:build windows

package secretstore

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var dpapiEntropy = []byte("ScriptBoard credential master key v1")

func wrapKey(raw []byte) ([]byte, error) {
	return cryptDPAPI(raw, true)
}

func unwrapKey(body []byte) ([]byte, error) {
	return cryptDPAPI(body, false)
}

func cryptDPAPI(input []byte, protect bool) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("DPAPI input is empty")
	}
	in := windows.DataBlob{Size: uint32(len(input)), Data: &input[0]}
	entropy := windows.DataBlob{Size: uint32(len(dpapiEntropy)), Data: &dpapiEntropy[0]}
	var out windows.DataBlob
	var err error
	if protect {
		description, convertErr := windows.UTF16PtrFromString("ScriptBoard credential master key")
		if convertErr != nil {
			return nil, convertErr
		}
		err = windows.CryptProtectData(&in, description, &entropy, 0, nil,
			windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	if out.Data == nil || out.Size == 0 {
		return nil, errors.New("DPAPI returned an empty value")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
