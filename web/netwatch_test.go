package main

import "testing"

func TestIsRelevantAndroidEvent(t *testing.T) {
	relevant := []string{
		`wifi_state_changed: [2,1]`,
		`wifi_network_connected: 1234,"Home"`,
		`battery_status: [3,95,4000]`,
		`power_saver_mode: [1]`,
		`connectivity_state_changed: [100,1]`,
		`default_network_changed: [100,101]`,
	}
	for _, line := range relevant {
		if !isRelevantAndroidEvent(line) {
			t.Errorf("expected %q to be relevant", line)
		}
	}

	irrelevant := []string{
		`am_proc_start: [0,1234,1000,com.example,activity]`,
		`am_activity_launch_time: [0,1234,com.example,.Main,100]`,
		`screen_toggled: 0`,
	}
	for _, line := range irrelevant {
		if isRelevantAndroidEvent(line) {
			t.Errorf("expected %q to be irrelevant", line)
		}
	}
}
