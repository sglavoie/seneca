package senecabridge

import (
	"os/exec"
	"strings"
	"testing"
)

// providerPackages are the model-provider SDKs and the desktop adapter that
// wraps them. A review never runs on the phone, so none of them may be linked
// into what gomobile binds.
var providerPackages = []string{
	"github.com/anthropics/",
	"github.com/openai/",
	"github.com/sglavoie/seneca/review/",
}

func listDeps(t *testing.T, pkg string) []string {
	t.Helper()
	output, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("listing the dependencies of %s: %v", pkg, err)
	}
	return strings.Fields(string(output))
}

func linksProvider(deps []string) []string {
	var found []string
	for _, dep := range deps {
		for _, prefix := range providerPackages {
			if strings.HasPrefix(dep, prefix) {
				found = append(found, dep)
			}
		}
	}
	return found
}

func TestTheBridgeLinksNoModelProviderSDK(t *testing.T) {
	// The desktop does link a provider SDK; seeing it there proves the listing
	// below would have caught one in the bridge.
	if len(linksProvider(listDeps(t, "github.com/sglavoie/seneca/cmd"))) == 0 {
		t.Fatal("expected github.com/sglavoie/seneca/cmd to link a provider SDK; this test can no longer see one")
	}
	for _, dep := range linksProvider(listDeps(t, ".")) {
		t.Errorf("the bridge links %s; provider SDKs are desktop-only", dep)
	}
}
