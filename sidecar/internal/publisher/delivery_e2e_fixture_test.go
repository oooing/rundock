package publisher

// Acceptance boundary: real Git repositories, candidate checks, subprocess
// builds, signatures, SQLite and artifact storage. Only GitHub is simulated.
// Failure contracts are recorded in docs/plans/2026-09-28-build-delivery.md.
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type deliveryGitRunner struct{ execRunner }

func (r deliveryGitRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" && len(args) >= 3 && strings.Join(args[len(args)-3:], " ") == "remote get-url origin" {
		return "https://github.com/fixture/project.git", nil
	}
	return r.execRunner.Run(ctx, dir, name, args...)
}

type deliveryFixture struct {
	svc                   *Service
	repo, counter, dbPath string
	gh                    *deliveryRemote
	request               CreateRequest
}

func newDeliveryFixture(t *testing.T, mode string, multi bool) *deliveryFixture {
	t.Helper()
	svc, repo, cleanup := newReleaseFixture(t)
	t.Cleanup(cleanup)
	base := filepath.Dir(repo)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(base, "signing-key.der")
	if err = os.WriteFile(keyPath, der, 0600); err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "public.txt"), base64.StdEncoding.EncodeToString(publicDER))
	counter := filepath.Join(base, "build-count.txt")
	t.Setenv("RUNDOCK_E2E_KEY", keyPath)
	t.Setenv("RUNDOCK_E2E_COUNT", counter)
	writeTestFile(t, filepath.Join(repo, "build.cjs"), `const fs=require('node:fs'),crypto=require('node:crypto');
const [id,mode]=process.argv.slice(2),v=JSON.parse(fs.readFileSync('package.json')).version;
fs.appendFileSync(process.env.RUNDOCK_E2E_COUNT,id+'\n');
fs.mkdirSync('out',{recursive:true});
if(mode==='wait'){setInterval(()=>{},1000)}else{
 const data=Buffer.from(JSON.stringify({platform:id,version:v}));
 const key=crypto.createPrivateKey({key:fs.readFileSync(process.env.RUNDOCK_E2E_KEY),format:'der',type:'pkcs8'});
 fs.writeFileSync('out/'+id+'-'+v+'.bin',data);
 if(mode!=='missing') fs.writeFileSync('out/'+id+'-'+v+'.sig',crypto.sign(null,data,key));
}
`)
	writeTestFile(t, filepath.Join(repo, "verify.cjs"), `const fs=require('node:fs'),crypto=require('node:crypto');
const [id,version]=process.argv.slice(2),data=fs.readFileSync('out/'+id+'-'+version+'.bin');
const payload=JSON.parse(data);if(payload.platform!==id||payload.version!==version)throw Error('wrong package');
const key=crypto.createPublicKey({key:Buffer.from(fs.readFileSync('public.txt','utf8'),'base64'),format:'der',type:'spki'});
if(!crypto.verify(null,data,key,fs.readFileSync('out/'+id+'-'+version+'.sig')))throw Error('signature mismatch');
`)
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "out/\n")
	cfg := &releaseconfig.Config{SchemaVersion: 1, VersionGroups: []releaseconfig.VersionGroup{{ID: "product", Name: "Fixture", VersionFiles: []releaseconfig.VersionFile{{Path: "package.json", Format: "json", JSONPointer: "/version"}}}}, CheckProfiles: []releaseconfig.CheckProfile{{ID: "fixture-check", Name: "Fixture source", Command: "node --check build.cjs", Required: true}}}
	ids := []string{"windows"}
	if multi {
		ids = append(ids, "android")
	}
	selections := []store.ReleaseTargetSelection{}
	for _, id := range ids {
		cfg.Targets = append(cfg.Targets, releaseconfig.Target{ID: id, Name: id, Kind: "desktop", VersionGroup: "product", WorkingDir: ".", Enabled: true, Runner: releaseconfig.Runner{Type: "local", OS: []string{}}, Steps: releaseconfig.Steps{Build: "node build.cjs " + id + " " + mode}, Artifacts: []string{"out/**"}, Timeouts: map[string]int{"build": 30}, ArtifactRules: []releaseconfig.ArtifactRule{{Pattern: "out/" + id + "-${VERSION}.bin", Min: 1, Max: 1}, {Pattern: "out/" + id + "-${VERSION}.sig", Min: 1, Max: 1}}, Verification: []releaseconfig.Verification{{Name: "package identity and signature", Command: "node verify.cjs " + id + " ${VERSION}", TimeoutSeconds: 10}}, Delivery: &releaseconfig.Delivery{Provider: "github", Repository: "fixture/project", Account: "fixture", WorkflowPolicy: "dispatch-only", MakeLatest: true}})
		selections = append(selections, store.ReleaseTargetSelection{TargetID: id, Build: true, Publish: true})
	}
	if mode == "timeout" {
		cfg.Targets[0].Steps.Build = "node build.cjs windows wait"
		cfg.Targets[0].Timeouts["build"] = 1
	}
	if mode == "rule-missing" {
		cfg.Targets[0].ArtifactRules = append(cfg.Targets[0].ArtifactRules, releaseconfig.ArtifactRule{Pattern: "out/required-extra.sig", Min: 1, Max: 1})
	}
	if mode == "verify-source-change" {
		cfg.Targets[0].Verification[0].Command += ` && node -e "require('fs').appendFileSync('tracked.txt','changed')"`
	}
	if mode == "workflow" {
		writeTestFile(t, filepath.Join(repo, ".github/workflows/release.yml"), "on: [push, release]\njobs: {}\n")
	}
	buildMode := "local"
	if mode == "cloud" {
		buildMode = "github"
		cfg.Targets[0].Runner.Type = "git-push"
		cfg.Targets[0].Steps = releaseconfig.Steps{Publish: "workflow-dispatch:release.yml"}
		cfg.Targets[0].Delivery = nil
		cfg.Targets[0].Verification = nil
		cfg.Targets[0].ArtifactRules = nil
		cfg.Automation = &releaseconfig.Automation{Provider: "github-actions", Trigger: "dispatch", Workflow: "release.yml", Account: "fixture", ReleaseBranch: "main", PublishesRelease: true}
		selections = []store.ReleaseTargetSelection{{TargetID: "windows", Publish: true}}
	}
	if _, err = svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "configure acceptance")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "release delivery acceptance\n")
	svc.runner = deliveryGitRunner{}
	gh := &deliveryRemote{repo: filepath.Join(base, "remote.git"), account: "fixture", assets: map[string]remoteAsset{}}
	svc.SetDeliveryClient(gh)
	request := acceptCandidate(t, svc, CreateRequest{SelectedPaths: []string{"tracked.txt"}, SelectedTargets: selections, VersionMode: "manual", TargetVersion: "1.0.1", CreateTag: boolPtr(true), PushRemote: boolPtr(true), BuildMode: buildMode, ExternalActionsConfirmed: true, ReleaseNotes: "Delivery acceptance", ReleaseNotesConfirmed: true})
	return &deliveryFixture{svc: svc, repo: repo, counter: counter, dbPath: filepath.Join(base, "launcher.db"), gh: gh, request: request}
}

func (f *deliveryFixture) start(t *testing.T) *store.ReleaseRun {
	t.Helper()
	run, err := f.svc.Start(context.Background(), "app1", f.request)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
func (f *deliveryFixture) settle(t *testing.T, id string) *store.ReleaseRun {
	t.Helper()
	run := waitRelease(t, f.svc, id)
	// The durable terminal row precedes deferred lock cleanup by a few instructions.
	for deadline := time.Now().Add(time.Second); f.svc.repositoryBusy(f.repo) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	return run
}
func (f *deliveryFixture) retry(t *testing.T, id string) *store.ReleaseRun {
	t.Helper()
	if _, err := f.svc.Retry(id); err != nil {
		t.Fatal(err)
	}
	return f.settle(t, id)
}
func (f *deliveryFixture) builds() int {
	raw, _ := os.ReadFile(f.counter)
	return strings.Count(string(raw), "\n")
}
func (f *deliveryFixture) restart(t *testing.T, id string) {
	t.Helper()
	if err := f.svc.store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.svc = New(db)
	f.svc.runner = deliveryGitRunner{}
	f.svc.SetDeliveryClient(f.gh)
	if err = f.svc.RecoverReleases(); err != nil {
		t.Fatal(err)
	}
}
