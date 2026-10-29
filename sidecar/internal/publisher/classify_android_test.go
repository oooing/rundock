package publisher

import (
	"errors"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func TestAndroidBuildSourcesAreRecommended(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "consumer-rules.pro", "proguard-rules.pro", "src/main/AndroidManifest.xml"} {
		rel := "code/mobile/modules/player-window/android/" + name
		item := classifyOne(repo, FileChange{Path: rel, Status: "??"}, nil)
		if item.Category != CategoryRecommend || !item.SelectedDefault {
			t.Fatalf("native source omitted: %+v", item)
		}
		for _, prefix := range []string{"outputs/", ".tmp/", "build/"} {
			output := classifyOne(repo, FileChange{Path: prefix + rel, Status: "??"}, nil)
			if output.Category != CategoryLocal || output.SelectedDefault {
				t.Fatalf("build output included: %+v", output)
			}
		}
		excluded := classifyOne(repo, FileChange{Path: rel, Status: "??"}, []releaseconfig.FileRule{{ID: "exclude", Pattern: rel, Kind: releaseconfig.RuleLocal}})
		if excluded.SelectedDefault {
			t.Fatalf("explicit exclusion overridden: %+v", excluded)
		}
	}
}

func TestNativeModuleFailureExplainsRecovery(t *testing.T) {
	for _, kind := range []string{"SOURCE", "MODULE"} {
		marker := "[ANDROID_NATIVE_" + kind + "_MISSING]"
		code, message := targetCommandFailure("Android", "build", marker+" secret-value", errors.New("exit 1"))
		if code != "build_native_"+strings.ToLower(kind)+"_missing" || !strings.Contains(message, "返回检查") || strings.Contains(message, "secret-value") {
			t.Fatalf("failure is not actionable: %s %s", code, message)
		}
	}
}
