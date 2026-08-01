package credential

import (
	"log"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// --- Windows CREDENTIAL API ---

type credentialType uint32

const (
	credTypeGeneric credentialType = 1
)

const credEnumAllCredentials uint32 = 1

type credPersist uint32

const (
	credPersistLocalMachine credPersist = 2
)

// CREDENTIALW 表示 Windows CREDENTIAL 结构体
type CREDENTIALW struct {
	Flags              uint32
	Type               credentialType
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            credPersist
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	modadvapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procCredWriteW     = modadvapi32.NewProc("CredWriteW")
	procCredReadW      = modadvapi32.NewProc("CredReadW")
	procCredFree       = modadvapi32.NewProc("CredFree")
	procCredDeleteW    = modadvapi32.NewProc("CredDeleteW")
	procCredEnumerateW = modadvapi32.NewProc("CredEnumerateW")
)

func winCredWrite(targetName, userName, secret string) error {
	target, err := syscall.UTF16PtrFromString(targetName)
	if err != nil {
		return err
	}

	blob := []byte(secret)
	u, _ := syscall.UTF16PtrFromString(userName)
	cred := &CREDENTIALW{
		Type:               credTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMachine,
		UserName:           u,
	}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}

	ret, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(cred)), 0)
	if ret == 0 {
		return err
	}
	return nil
}

func winCredRead(targetName string) (string, error) {
	target, err := syscall.UTF16PtrFromString(targetName)
	if err != nil {
		return "", err
	}

	var pcred unsafe.Pointer
	ret, _, err := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
		uintptr(unsafe.Pointer(&pcred)),
	)
	if ret == 0 {
		return "", err
	}

	cred := (*CREDENTIALW)(pcred)
	var result string
	if cred.CredentialBlobSize > 0 {
		blob := make([]byte, cred.CredentialBlobSize)
		copy(blob, unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize))
		result = string(blob)
	}

	procCredFree.Call(uintptr(pcred))
	return result, nil
}

func winCredDelete(targetName string) error {
	target, err := syscall.UTF16PtrFromString(targetName)
	if err != nil {
		return err
	}

	ret, _, err := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(credTypeGeneric),
		0,
	)
	if ret == 0 {
		return err
	}
	return nil
}

// winCredEnumerate 枚举目标名以 prefix 开头的所有通用凭据，返回完整目标名列表。
// Windows 的 Generic 凭据 TargetName 带有 "LegacyGeneric:target=" 包装，此处剥离后再匹配。
func winCredEnumerate(prefix string) ([]string, error) {
	var count uint32
	var pcreds unsafe.Pointer
	ret, _, err := procCredEnumerateW.Call(
		0, // filter = NULL，枚举全部
		uintptr(credEnumAllCredentials),
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&pcreds)),
	)
	if ret == 0 {
		return nil, err
	}
	defer procCredFree.Call(uintptr(pcreds))

	elems := unsafe.Slice((*unsafe.Pointer)(pcreds), int(count))
	var result []string
	for _, e := range elems {
		cred := (*CREDENTIALW)(e)
		if cred.TargetName == nil {
			continue
		}
		target := windows.UTF16PtrToString(cred.TargetName)
		// 剥离 Windows 为 Generic 凭据添加的 "LegacyGeneric:target=" 包装前缀
		target = strings.TrimPrefix(target, "LegacyGeneric:target=")
		if strings.HasPrefix(target, prefix) {
			result = append(result, target)
		}
	}
	return result, nil
}

// EnsureCredentialAPI 检查 Windows Credential API 是否可用
func EnsureCredentialAPI() bool {
	err := modadvapi32.Load()
	if err != nil {
		log.Printf("[Cred] 无法加载 advapi32.dll: %v", err)
		return false
	}
	return true
}
