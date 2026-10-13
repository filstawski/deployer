package secretfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filstawski/deployer/internal/domain"
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
	// Formatting both the same way abbreviates well-known accounts alike.
	user, _ := currentUser()
	want, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	if sd.String() != want.String() {
		t.Fatalf("access list %s, want %s", sd, want)
	}
}

// A file in the user's profile, with the access list it inherits from there,
// is private.
func TestProfileFilesArePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	secret := domain.NewTokenSecret()
	if err := os.WriteFile(path, []byte(secret+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadToken(path); err != nil || got != secret {
		t.Fatalf("ReadToken = %q, %v", got, err)
	}
}
