package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"sync"
	"time"
)

type Service struct {
	delivery         *delivery.Engine
	fileLocks        map[string]func()
	runCancels       map[string]context.CancelFunc
	cancelRequested  map[string]bool
	candidateCommand func(context.Context, string, releaseconfig.CheckProfile) (string, string, string)
	store            *store.Store
	releaseConfig    *releaseconfig.Service
	runner           commandRunner
	targetRunner     commandRunner
	mu               sync.Mutex
	active           map[string]bool
	restarting       bool // Serializes restart with all local build/check/release reservations.
	diagnostics      *diagnostics.Service
	candidatesMu     sync.Mutex
	candidates       map[string]*releaseCandidate
	// Set before monitoring starts. Called only after a changed snapshot is saved.
	OnCloudBuildChange func(*store.CloudBuild)
}

const releasePushTimeout = 2 * time.Minute

func New(st *store.Store) *Service {
	return &Service{delivery: delivery.New(st), store: st, releaseConfig: releaseconfig.New(st), runner: execRunner{}, targetRunner: execRunner{}, active: map[string]bool{}, candidates: map[string]*releaseCandidate{}}
}

// SetDiagnostics injects the optional project-local diagnostic sink. Writing
// diagnostics is best effort and never changes a release result.
func (s *Service) SetDiagnostics(value *diagnostics.Service) { s.diagnostics = value }
