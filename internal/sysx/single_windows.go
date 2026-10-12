package sysx

import "golang.org/x/sys/windows"

// acquire: a named mutex in the user's session ("Local\").
func acquire(name string) (func(), bool) {
	p, err := windows.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return nil, true // cannot tell: never block the app
	}
	h, err := windows.CreateMutex(nil, false, p)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, false
	}
	if err != nil || h == 0 {
		return nil, true
	}
	return func() { windows.CloseHandle(h) }, true
}
