package printing_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/RayFlows/printbridge/server/internal/printing"
)

func TestDeviceProfileControlsBothBindingEdgesAndOrientations(t *testing.T) {
	for _, tc := range []struct {
		orientation, sides string
		rotation           int
	}{
		{"portrait", "manual-long-edge", 0},
		{"portrait", "manual-short-edge", 180},
		{"landscape", "manual-long-edge", 180},
		{"landscape", "manual-short-edge", 0},
	} {
		t.Run(tc.orientation+"/"+tc.sides, func(t *testing.T) {
			f := setup(t)
			s := settings()
			backend, err := printing.WithManualDuplexProfiles(f.backend,
				map[string]string{s.PrinterID: printing.RearFaceUpProfile})
			if err != nil {
				t.Fatal(err)
			}
			f.service, err = printing.New(f.root, f.db, source{}, f.processor, backend)
			if err != nil {
				t.Fatal(err)
			}
			s.Pages, s.OutputOrder = "1-3", "reverse"
			s.Sides, s.Orientation = tc.sides, tc.orientation
			s.ManualDuplexProfile = printing.RearFaceUpProfile
			s.ManualFirstSide, s.BackOrder, s.BackRotation, s.Calibrated = "front", "reverse", 180, false
			plan, err := f.service.Prepare(context.Background(), "owner", "doc", s, "")
			if err != nil {
				t.Fatal(err)
			}
			if plan.Settings.FirstPhase() != "back" || plan.Settings.BackOrder != "same" ||
				!plan.Settings.Calibrated || f.backend.creates != 0 {
				t.Fatal("client calibration overrode profile or preview printed", plan.Settings)
			}
			f.processor.phasePages, f.processor.phaseRotations = nil, nil
			job := f.create(t, plan, "request-0000000001")
			if !reflect.DeepEqual(f.processor.phasePages, [][]int{{2, 4}, {1, 3}}) ||
				!reflect.DeepEqual(f.processor.phaseRotations, []int{0, tc.rotation}) {
				t.Fatal("incorrect faces or binding orientation", f.processor.phasePages, f.processor.phaseRotations)
			}
			// The saved plan, not current deployment configuration, governs a retry.
			f.service, err = printing.New(f.root, f.db, source{}, f.processor, f.backend)
			if err != nil {
				t.Fatal(err)
			}
			recovered := f.job(t, job.ID)
			if recovered.Settings != plan.Settings {
				t.Fatal("saved profile/settings lost after restart")
			}
		})
	}
}

func TestProfilesAreExplicitPerQueueAndInvalidProfilesFail(t *testing.T) {
	f := setup(t)
	s := settings()
	s.Sides, s.ManualDuplexProfile = "manual-long-edge", printing.RearFaceUpProfile
	if _, err := f.service.Prepare(context.Background(), "owner", "doc", s, ""); !errors.Is(err, printing.ErrSettings) {
		t.Fatal("unconfigured queue accepted a claimed profile", err)
	}
	for _, profiles := range []map[string]string{
		{"": printing.RearFaceUpProfile}, {"virtual": "unknown"}, {"virtual": ""},
	} {
		if _, err := printing.WithManualDuplexProfiles(f.backend, profiles); err == nil {
			t.Fatal("invalid deployment profile accepted")
		}
	}
	profiles := map[string]string{"another-queue": printing.RearFaceUpProfile}
	backend, err := printing.WithManualDuplexProfiles(f.backend, profiles)
	if err != nil {
		t.Fatal(err)
	}
	profiles[s.PrinterID] = printing.RearFaceUpProfile
	list, err := backend.List(context.Background())
	if err != nil || len(list) == 0 || list[0].ManualDuplexProfile != "" {
		t.Fatal("profile leaked to other queue or external mutation changed configuration", list, err)
	}
}
