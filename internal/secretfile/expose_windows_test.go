package secretfile

import (
	"testing"

	"golang.org/x/sys/windows"
)

// exposeToOthers adds an entry that lets Everyone read path.
func exposeToOthers(t *testing.T, path string) {
	t.Helper()
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// Files Deployer writes allow only the current user.
func TestPrivateAccessList(t *testing.T) {
	path := t.TempDir() + `\token`
	if err := WriteNew(path, "x"); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, _ := currentUser()
	if got, want := sd.String(), "D:P(A;;FA;;;"+user.String()+")"; got != want {
		t.Fatalf("access list %s, want %s", got, want)
	}
}
