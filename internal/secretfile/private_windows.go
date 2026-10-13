package secretfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no permission bits; access lists take their place. Secret
// files are created with an access list that allows only the current user,
// and files that anyone else can read are refused. SYSTEM and Administrators
// may appear, as they do on every user's profile folder.

// createPrivate creates path for writing, accessible only by the current
// user, failing if it exists.
func createPrivate(path string) (*os.File, error) {
	user, err := currentUser()
	if err != nil {
		return nil, err
	}
	// P stops the folder's entries from being inherited; the only entry
	// gives the user full access.
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.String() + ")")
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// readAccess are the rights that allow reading a file's contents.
const readAccess = windows.FILE_READ_DATA | windows.GENERIC_READ | windows.GENERIC_ALL

// checkPrivate refuses a file whose access list lets other users read it.
func checkPrivate(path string, _ fs.FileInfo) error {
	user, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot check who can read %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil && !errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return fmt.Errorf("cannot check who can read %s: %w", path, err)
	}
	if dacl == nil { // No access list allows everyone.
		return exposed(path, user)
	}
	trusted := []*windows.SID{user}
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		if sid, err := windows.CreateWellKnownSid(kind); err == nil {
			trusted = append(trusted, sid)
		}
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("cannot check who can read %s: %w", path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Mask&readAccess == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !containsSID(trusted, sid) {
			return exposed(path, user)
		}
	}
	return nil
}

func exposed(path string, user *windows.SID) error {
	name := "%USERNAME%"
	if account, domain, _, err := user.LookupAccount(""); err == nil {
		name = domain + `\` + account
	}
	return fmt.Errorf(`%s is readable by other users; allow only yourself with: icacls "%s" /inheritance:r /grant:r "%s:F"`, path, path, name)
}

func containsSID(list []*windows.SID, sid *windows.SID) bool {
	for _, s := range list {
		if s.Equals(sid) {
			return true
		}
	}
	return false
}

func currentUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cannot find the current Windows user: %w", err)
	}
	return u.User.Sid, nil
}
