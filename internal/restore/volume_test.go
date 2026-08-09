package restore

import "testing"

func TestDecideRunningAction(t *testing.T) {
	tests := []struct {
		name  string
		force bool
		stop  bool
		want  runningAction
	}{
		{"no flags refuses", false, false, actionRefuse},
		{"force proceeds", true, false, actionProceed},
		{"stop stops and restarts", false, true, actionStopRestart},
		{"stop wins over force", true, true, actionStopRestart},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideRunningAction(tt.force, tt.stop); got != tt.want {
				t.Errorf("decideRunningAction(force=%v, stop=%v) = %v, want %v", tt.force, tt.stop, got, tt.want)
			}
		})
	}
}
