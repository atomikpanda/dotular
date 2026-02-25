package actions

import "testing"

func TestSettingActionDescribe(t *testing.T) {
	a := &SettingAction{Domain: "com.apple.dock", Key: "autohide", Value: true}
	got := a.Describe()
	want := "set com.apple.dock autohide = true"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestMacOSValueArgs(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		wantType string
		wantVal  string
	}{
		{"bool true", true, "-bool", "true"},
		{"bool false", false, "-bool", "false"},
		{"int", 42, "-int", "42"},
		{"float", 3.14, "-float", "3.14"},
		{"string", "hello", "-string", "hello"},
		{"other", []int{1}, "-string", "[1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typeFlag, val := macOSValueArgs(tt.value)
			if typeFlag != tt.wantType {
				t.Errorf("typeFlag = %q, want %q", typeFlag, tt.wantType)
			}
			if val != tt.wantVal {
				t.Errorf("val = %q, want %q", val, tt.wantVal)
			}
		})
	}
}
