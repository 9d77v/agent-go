//go:build !windows

package credential

func winCredWrite(target, userName, secret string) error { return nil }
func winCredRead(target string) (string, error) { return "", nil }
func winCredDelete(target string) error { return nil }
