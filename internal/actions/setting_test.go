package actions

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"testing"
	"time"
	"unicode/utf16"
)

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

func TestSettingActionRunDryRun(t *testing.T) {
	a := &SettingAction{Domain: "com.apple.dock", Key: "autohide", Value: true}
	if err := a.Run(context.Background(), true); err != nil {
		t.Errorf("dry run error: %v", err)
	}
}

func TestSettingActionRunDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only test")
	}
	// Use a temporary domain that won't affect the real system.
	a := &SettingAction{Domain: "com.dotular.test", Key: "testkey", Value: "testval"}
	err := a.Run(context.Background(), false)
	// defaults write should succeed on macOS.
	if err != nil {
		t.Errorf("Run error: %v", err)
	}
}

func TestWindowsRegistryArgs(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		wantType string
		wantVal  string
	}{
		{"string", "hello", "REG_SZ", "hello"},
		{"int", 42, "REG_DWORD", "42"},
		{"bool true", true, "REG_DWORD", "1"},
		{"bool false", false, "REG_DWORD", "0"},
		{"float", 3.14, "REG_SZ", "3.14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			regType, regVal := windowsValueArgs(tt.value)
			if regType != tt.wantType {
				t.Errorf("regType = %q, want %q", regType, tt.wantType)
			}
			if regVal != tt.wantVal {
				t.Errorf("regVal = %q, want %q", regVal, tt.wantVal)
			}
		})
	}
}

func TestSettingsSupported(t *testing.T) {
	tests := []struct {
		goos string
		want bool
	}{
		{"darwin", true},
		{"windows", true},
		{"linux", false},
		{"freebsd", false},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := SettingsSupported(tt.goos); got != tt.want {
				t.Errorf("SettingsSupported(%q) = %v, want %v", tt.goos, got, tt.want)
			}
		})
	}
}

func TestSettingActionRunLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only test")
	}
	a := &SettingAction{Domain: "test", Key: "k", Value: "v"}
	err := a.Run(context.Background(), false)
	if err == nil {
		t.Error("expected error on linux")
	}
}

type settingCommandCall struct {
	ctx           context.Context
	args          []string
	captureOutput bool
}

type recordingSettingExecutor struct {
	results []packageCommandResult
	calls   []settingCommandCall
}

func (e *recordingSettingExecutor) execute(ctx context.Context, args []string, captureOutput bool) ([]byte, error) {
	e.calls = append(e.calls, settingCommandCall{
		ctx:           ctx,
		args:          append([]string(nil), args...),
		captureOutput: captureOutput,
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(e.results) == 0 {
		return nil, errors.New("unexpected setting command")
	}
	result := e.results[0]
	e.results = e.results[1:]
	return result.output, result.err
}

func settingExitError(stderr string) error {
	return &exec.ExitError{Stderr: []byte(stderr)}
}

func assertSettingCalls(t *testing.T, got []settingCommandCall, want ...packageCommandCall) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i].args, want[i].args) {
			t.Errorf("call %d args = %#v, want %#v", i, got[i].args, want[i].args)
		}
		if got[i].captureOutput != want[i].captureOutput {
			t.Errorf("call %d captureOutput = %v, want %v", i, got[i].captureOutput, want[i].captureOutput)
		}
	}
}

func TestSettingActionPrepareMacOSCompensationRestoresExactScalar(t *testing.T) {
	tests := []struct {
		name        string
		typeOutput  string
		valueOutput string
		typeFlag    string
		value       string
	}{
		{name: "string", typeOutput: "Type is string\n", valueOutput: "hello  world\n", typeFlag: "-string", value: "hello  world"},
		{name: "string ending carriage return", typeOutput: "Type is string\n", valueOutput: "old\r\n", typeFlag: "-string", value: "old\r"},
		{name: "boolean", typeOutput: "Type is boolean\n", valueOutput: "1\n", typeFlag: "-bool", value: "1"},
		{name: "integer", typeOutput: "Type is integer\n", valueOutput: "-42\n", typeFlag: "-int", value: "-42"},
		{name: "float", typeOutput: "Type is float\n", valueOutput: "3.125\n", typeFlag: "-float", value: "3.125"},
		{name: "real", typeOutput: "Type is real\n", valueOutput: "3.125\n", typeFlag: "-float", value: "3.125"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingSettingExecutor{results: []packageCommandResult{
				{output: []byte(tt.typeOutput)},
				{output: []byte(tt.valueOutput)},
				{},
			}}
			action := &SettingAction{
				Domain:   "com.dotular.test",
				Key:      "sample",
				Value:    "new",
				OS:       "darwin",
				executor: executor.execute,
			}

			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if preparation.Compensation == nil {
				t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
			}
			if preparation.UnavailableReason != "" {
				t.Fatalf("UnavailableReason = %q", preparation.UnavailableReason)
			}
			if err := preparation.Compensation.Run(context.Background()); err != nil {
				t.Fatal(err)
			}

			assertSettingCalls(t, executor.calls,
				packageCommandCall{args: []string{"defaults", "read-type", "com.dotular.test", "sample"}, captureOutput: true},
				packageCommandCall{args: []string{"defaults", "read", "com.dotular.test", "sample"}, captureOutput: true},
				packageCommandCall{args: []string{"defaults", "write", "com.dotular.test", "sample", tt.typeFlag, tt.value}},
			)
		})
	}
}

func TestSettingActionPrepareMacOSCompensationDeletesProvenMissingKey(t *testing.T) {
	executor := &recordingSettingExecutor{results: []packageCommandResult{
		{err: settingExitError("The domain/default pair of (com.dotular.test, sample) does not exist\n")},
		{},
	}}
	action := &SettingAction{
		Domain:   "com.dotular.test",
		Key:      "sample",
		Value:    "new",
		OS:       "darwin",
		executor: executor.execute,
	}

	preparation, err := action.PrepareCompensation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preparation.Compensation == nil {
		t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
	}
	if err := preparation.Compensation.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertSettingCalls(t, executor.calls,
		packageCommandCall{args: []string{"defaults", "read-type", "com.dotular.test", "sample"}, captureOutput: true},
		packageCommandCall{args: []string{"defaults", "delete", "com.dotular.test", "sample"}},
	)
}

func TestSettingActionPrepareMacOSCompensationLeavesInconclusiveStateUnknown(t *testing.T) {
	tests := []struct {
		name    string
		results []packageCommandResult
	}{
		{
			name:    "permission error",
			results: []packageCommandResult{{err: settingExitError("Permission denied\n")}},
		},
		{
			name:    "missing defaults binary",
			results: []packageCommandResult{{err: &exec.Error{Name: "defaults", Err: os.ErrNotExist}}},
		},
		{
			name:    "unexpected status",
			results: []packageCommandResult{{err: errors.New("exit status 7")}},
		},
		{
			name:    "malformed type output",
			results: []packageCommandResult{{output: []byte("unexpected output\n")}},
		},
		{
			name:    "unsupported array type",
			results: []packageCommandResult{{output: []byte("Type is array\n")}},
		},
		{
			name: "malformed boolean value",
			results: []packageCommandResult{
				{output: []byte("Type is boolean\n")},
				{output: []byte("not-a-boolean\n")},
			},
		},
		{
			name: "malformed integer value",
			results: []packageCommandResult{
				{output: []byte("Type is integer\n")},
				{output: []byte("not-an-integer\n")},
			},
		},
		{
			name: "malformed float value",
			results: []packageCommandResult{
				{output: []byte("Type is float\n")},
				{output: []byte("not-a-float\n")},
			},
		},
		{
			name: "value read failure",
			results: []packageCommandResult{
				{output: []byte("Type is string\n")},
				{err: settingExitError("Permission denied\n")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingSettingExecutor{results: tt.results}
			action := &SettingAction{
				Domain:   "com.dotular.test",
				Key:      "sample",
				Value:    "new",
				OS:       "darwin",
				executor: executor.execute,
			}

			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if preparation.Compensation != nil {
				t.Fatal("inconclusive state prepared a compensation")
			}
			if preparation.UnavailableReason == "" {
				t.Fatal("UnavailableReason is empty")
			}
		})
	}
}

func TestSettingActionPrepareWindowsCompensationRestoresExactRegistryValue(t *testing.T) {
	tests := []struct {
		name      string
		valueType uint32
		data      []byte
		regType   string
		value     string
	}{
		{name: "string", valueType: 1, data: nativeRegistryStringBytes("hello  world"), regType: "REG_SZ", value: "hello  world"},
		{name: "dword", valueType: 4, data: nativeRegistryDWORDBytes(42), regType: "REG_DWORD", value: "0x0000002a"},
		{name: "qword", valueType: 11, data: nativeRegistryQWORDBytes(42), regType: "REG_QWORD", value: "0x000000000000002a"},
		{name: "binary", valueType: 3, data: []byte{0xde, 0xad, 0xbe, 0xef}, regType: "REG_BINARY", value: "deadbeef"},
		{name: "multi string", valueType: 7, data: nativeRegistryMultiStringBytes("one", "two"), regType: "REG_MULTI_SZ", value: `one\0two`},
		{name: "empty multi string", valueType: 7, data: nativeRegistryMultiStringBytes(), regType: "REG_MULTI_SZ", value: ""},
		{name: "leading spaces", valueType: 1, data: nativeRegistryStringBytes("  padded"), regType: "REG_SZ", value: "  padded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingSettingExecutor{results: []packageCommandResult{{}}}
			action := &SettingAction{
				Domain:   `HKCU\Software\Dotular`,
				Key:      "sample",
				Value:    "new",
				OS:       "windows",
				executor: executor.execute,
				windowsReader: func(ctx context.Context, domain, key string) (windowsRegistryValueState, error) {
					if domain != `HKCU\Software\Dotular` || key != "sample" {
						t.Fatalf("native reader got domain/key %q/%q", domain, key)
					}
					return windowsRegistryValueState{present: true, valueType: tt.valueType, data: tt.data}, nil
				},
			}

			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if preparation.Compensation == nil {
				t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
			}
			if err := preparation.Compensation.Run(context.Background()); err != nil {
				t.Fatal(err)
			}

			assertSettingCalls(t, executor.calls,
				packageCommandCall{args: []string{"reg", "add", `HKCU\Software\Dotular`, "/v", "sample", "/t", tt.regType, "/d", tt.value, "/f"}},
			)
		})
	}
}

func TestSettingActionPrepareWindowsCompensationDeletesProvenMissingValue(t *testing.T) {
	executor := &recordingSettingExecutor{results: []packageCommandResult{{}}}
	action := &SettingAction{
		Domain:   `HKCU\Software\Dotular`,
		Key:      "sample",
		Value:    "new",
		OS:       "windows",
		executor: executor.execute,
		windowsReader: func(context.Context, string, string) (windowsRegistryValueState, error) {
			return windowsRegistryValueState{}, nil
		},
	}

	preparation, err := action.PrepareCompensation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preparation.Compensation == nil {
		t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
	}
	if err := preparation.Compensation.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertSettingCalls(t, executor.calls,
		packageCommandCall{args: []string{"reg", "delete", `HKCU\Software\Dotular`, "/v", "sample", "/f"}},
	)
}

func TestSettingActionPrepareWindowsCompensationDeletesCreatedRegistryKeyTree(t *testing.T) {
	executor := &recordingSettingExecutor{results: []packageCommandResult{{}}}
	action := &SettingAction{
		Domain:   `HKCU\Software\Dotular`,
		Key:      "sample",
		Value:    "new",
		OS:       "windows",
		executor: executor.execute,
		windowsReader: func(context.Context, string, string) (windowsRegistryValueState, error) {
			return windowsRegistryValueState{deleteKey: `HKCU\Software\Dotular`}, nil
		},
	}

	preparation, err := action.PrepareCompensation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preparation.Compensation == nil {
		t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
	}
	if err := preparation.Compensation.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertSettingCalls(t, executor.calls,
		packageCommandCall{args: []string{"reg", "delete", `HKCU\Software\Dotular`, "/f"}},
	)
}

func TestSettingActionPrepareWindowsCompensationLeavesInconclusiveStateUnknown(t *testing.T) {
	tests := []struct {
		name      string
		present   bool
		valueType uint32
		data      []byte
		err       error
	}{
		{name: "permission error", err: errors.New("access denied")},
		{name: "malformed registry path", err: errors.New("unsupported registry hive")},
		{name: "resource list type", present: true, valueType: 8, data: []byte{1}},
		{name: "resource requirements type", present: true, valueType: 10, data: []byte{1}},
		{name: "none type", present: true, valueType: 0, data: []byte{1}},
		{name: "malformed string data", present: true, valueType: 1, data: []byte{1}},
		{name: "malformed dword data", present: true, valueType: 4, data: []byte{1}},
		{name: "unpaired UTF-16 surrogate", present: true, valueType: 1, data: []byte{0x00, 0xd8, 0x00, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := &SettingAction{
				Domain: "domain",
				Key:    "sample",
				Value:  "new",
				OS:     "windows",
				windowsReader: func(context.Context, string, string) (windowsRegistryValueState, error) {
					return windowsRegistryValueState{present: tt.present, valueType: tt.valueType, data: tt.data}, tt.err
				},
			}

			preparation, err := action.PrepareCompensation(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if preparation.Compensation != nil {
				t.Fatal("inconclusive state prepared a compensation")
			}
			if preparation.UnavailableReason == "" {
				t.Fatal("UnavailableReason is empty")
			}
		})
	}
}

func TestSettingActionPrepareCompensationPropagatesCanceledCapture(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{name: "canceled", ctx: canceledSettingContext(), err: context.Canceled},
		{name: "deadline", ctx: expiredSettingContext(), err: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingSettingExecutor{}
			action := &SettingAction{OS: "darwin", executor: executor.execute}
			_, err := action.PrepareCompensation(tt.ctx)
			if !errors.Is(err, tt.err) {
				t.Fatalf("PrepareCompensation() error = %v, want %v", err, tt.err)
			}
			if len(executor.calls) != 0 {
				t.Fatalf("capture executed %d commands after context cancellation", len(executor.calls))
			}
		})
	}
}

func canceledSettingContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredSettingContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	return ctx
}

func TestSettingCompensationUsesFreshRollbackContextAndReportsFailures(t *testing.T) {
	tests := []struct {
		name           string
		os             string
		capture        []packageCommandResult
		windowsMissing bool
		rollbackErr    error
	}{
		{
			name: "macOS restore failure",
			os:   "darwin",
			capture: []packageCommandResult{
				{output: []byte("Type is string\n")},
				{output: []byte("old\n")},
			},
			rollbackErr: errors.New("defaults write failed"),
		},
		{
			name:           "Windows delete failure",
			os:             "windows",
			windowsMissing: true,
			rollbackErr:    errors.New("reg delete failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := append([]packageCommandResult(nil), tt.capture...)
			results = append(results, packageCommandResult{err: tt.rollbackErr})
			executor := &recordingSettingExecutor{results: results}
			action := &SettingAction{
				Domain:   "domain",
				Key:      "key",
				Value:    "new",
				OS:       tt.os,
				executor: executor.execute,
			}
			if tt.windowsMissing {
				action.windowsReader = func(context.Context, string, string) (windowsRegistryValueState, error) {
					return windowsRegistryValueState{}, nil
				}
			}
			captureCtx := context.WithValue(context.Background(), settingContextKey{}, "capture")
			preparation, err := action.PrepareCompensation(captureCtx)
			if err != nil {
				t.Fatal(err)
			}
			if preparation.Compensation == nil {
				t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
			}

			rollbackCtx := context.WithValue(context.Background(), settingContextKey{}, "rollback")
			err = preparation.Compensation.Run(rollbackCtx)
			if !errors.Is(err, tt.rollbackErr) {
				t.Fatalf("Run() error = %v, want wrapped %v", err, tt.rollbackErr)
			}
			if got := executor.calls[len(executor.calls)-1].ctx; got != rollbackCtx {
				t.Fatal("rollback command did not receive the context supplied to Compensation.Run")
			}
		})
	}
}

type settingContextKey struct{}

func TestSettingActionRunUsesExplicitOSAndExecutor(t *testing.T) {
	tests := []struct {
		name string
		os   string
		args []string
	}{
		{
			name: "macOS",
			os:   "darwin",
			args: []string{"defaults", "write", "domain", "key", "-string", "value"},
		},
		{
			name: "Windows",
			os:   "windows",
			args: []string{"reg", "add", "domain", "/v", "key", "/t", "REG_SZ", "/d", "value", "/f"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingSettingExecutor{results: []packageCommandResult{{}}}
			action := &SettingAction{
				Domain:   "domain",
				Key:      "key",
				Value:    "value",
				OS:       tt.os,
				executor: executor.execute,
			}
			if err := action.Run(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			assertSettingCalls(t, executor.calls, packageCommandCall{args: tt.args})
		})
	}
}

func TestSettingActionPrepareWindowsCompensationReadsNativeUnicodeValue(t *testing.T) {
	const testWindowsRegistryStringType = 1

	read := false
	executor := &recordingSettingExecutor{results: []packageCommandResult{{}}}
	action := &SettingAction{
		Domain:   `HKCU\Software\Dotular`,
		Key:      "sample",
		Value:    "new",
		OS:       "windows",
		executor: executor.execute,
		windowsReader: func(ctx context.Context, domain, key string) (windowsRegistryValueState, error) {
			read = true
			if domain != `HKCU\Software\Dotular` || key != "sample" {
				t.Fatalf("native reader got domain/key %q/%q", domain, key)
			}
			return windowsRegistryValueState{
				present:   true,
				valueType: testWindowsRegistryStringType,
				data:      nativeRegistryStringBytes("日本語 café"),
			}, nil
		},
	}

	preparation, err := action.PrepareCompensation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preparation.Compensation == nil {
		t.Fatalf("Compensation = nil, unavailable reason %q", preparation.UnavailableReason)
	}
	if !read {
		t.Fatal("native registry reader was not called")
	}
	if err := preparation.Compensation.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertSettingCalls(t, executor.calls,
		packageCommandCall{args: []string{"reg", "add", `HKCU\Software\Dotular`, "/v", "sample", "/t", "REG_SZ", "/d", "日本語 café", "/f"}},
	)
}

func TestSettingActionPrepareWindowsCompensationChecksContextAfterNativeRead(t *testing.T) {
	const testWindowsRegistryStringType = 1

	ctx, cancel := context.WithCancel(context.Background())
	action := &SettingAction{
		Domain: "domain",
		Key:    "key",
		OS:     "windows",
		windowsReader: func(context.Context, string, string) (windowsRegistryValueState, error) {
			cancel()
			return windowsRegistryValueState{
				present:   true,
				valueType: testWindowsRegistryStringType,
				data:      nativeRegistryStringBytes("old"),
			}, nil
		},
	}

	_, err := action.PrepareCompensation(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareCompensation() error = %v, want context.Canceled", err)
	}
}

func nativeRegistryStringBytes(value string) []byte {
	encoded := utf16.Encode([]rune(value))
	encoded = append(encoded, 0)
	return nativeRegistryCodeUnitBytes(encoded)
}

func nativeRegistryMultiStringBytes(values ...string) []byte {
	var encoded []uint16
	for _, value := range values {
		encoded = append(encoded, utf16.Encode([]rune(value))...)
		encoded = append(encoded, 0)
	}
	if len(values) == 0 {
		encoded = append(encoded, 0)
	}
	encoded = append(encoded, 0)
	return nativeRegistryCodeUnitBytes(encoded)
}

func nativeRegistryDWORDBytes(value uint32) []byte {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, value)
	return data
}

func nativeRegistryQWORDBytes(value uint64) []byte {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, value)
	return data
}

func nativeRegistryCodeUnitBytes(encoded []uint16) []byte {
	data := make([]byte, len(encoded)*2)
	for i, codeUnit := range encoded {
		binary.LittleEndian.PutUint16(data[i*2:], codeUnit)
	}
	return data
}
