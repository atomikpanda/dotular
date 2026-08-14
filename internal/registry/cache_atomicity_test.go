package registry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const (
	cacheObservationRounds = 48
	cacheReadsPerRound     = 24
)

func TestWriteCacheFilePublishesWithoutTempLitter(t *testing.T) {
	tests := []struct {
		name            string
		seedDestination bool
	}{
		{
			name:            "creates absent destination",
			seedDestination: false,
		},
		{
			name:            "replaces existing destination",
			seedDestination: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "cache.json")
			if test.seedDestination {
				writeTestFile(t, path, []byte("complete old bytes"))
			}

			newData := []byte("complete new bytes")
			if err := writeCacheFile(path, newData); err != nil {
				t.Fatalf("writeCacheFile() error = %v", err)
			}

			assertTestFileData(t, path, newData)
			assertDirectoryNames(t, dir, []string{"cache.json"})
		})
	}
}

func TestWriteCacheFileAbsentDestinationAtomicVisibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	newData := append(bytes.Repeat([]byte("new-entry-"), 16*1024), []byte("complete-new-end")...)

	for round := range cacheObservationRounds {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("round %d: os.Remove(%q) error = %v", round, path, err)
		}

		runCacheObservationRound(
			t,
			path,
			func(data []byte, readErr error) error {
				if errors.Is(readErr, os.ErrNotExist) {
					return nil
				}
				if readErr != nil {
					return fmt.Errorf("read absent-destination cache: %w", readErr)
				}
				if !bytes.Equal(data, newData) {
					return fmt.Errorf(
						"observed %d bytes that were neither absence nor the complete %d-byte payload",
						len(data),
						len(newData),
					)
				}
				return nil
			},
			func() error {
				return writeCacheFile(path, newData)
			},
		)

		assertTestFileData(t, path, newData)
		assertDirectoryNames(t, dir, []string{"cache.json"})
	}
}

func TestWriteCacheFileReplacementAtomicVisibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	oldData := append(bytes.Repeat([]byte("old-entry-"), 12*1024), []byte("complete-old-end")...)
	newData := append(bytes.Repeat([]byte("new-entry-"), 16*1024), []byte("complete-new-end")...)
	writeTestFile(t, path, oldData)

	currentData := oldData
	for round := range cacheObservationRounds {
		nextData := newData
		if round%2 == 1 {
			nextData = oldData
		}

		runCacheObservationRound(
			t,
			path,
			func(data []byte, readErr error) error {
				if readErr != nil {
					return fmt.Errorf("replacement observer saw destination read failure: %w", readErr)
				}
				if !bytes.Equal(data, oldData) && !bytes.Equal(data, newData) {
					return fmt.Errorf(
						"replacement observer saw %d bytes that were neither the complete old nor complete new payload",
						len(data),
					)
				}
				return nil
			},
			func() error {
				return writeCacheFile(path, nextData)
			},
		)

		currentData = nextData
		assertTestFileData(t, path, currentData)
		assertDirectoryNames(t, dir, []string{"cache.json"})
	}
}

func runCacheObservationRound(
	t *testing.T,
	path string,
	validate func([]byte, error) error,
	write func() error,
) {
	t.Helper()

	type observationResult struct {
		writeErr             error
		observationErr       error
		postStartValidations int
	}

	observe := func() error {
		data, err := os.ReadFile(path)
		return validate(data, err)
	}

	initialValidation := make(chan error, 1)
	observerReady := make(chan struct{})
	writerEntered := make(chan struct{})
	writeDone := make(chan error, 1)
	result := make(chan observationResult, 1)

	go func() {
		initialErr := observe()
		initialValidation <- initialErr
		if initialErr != nil {
			return
		}

		close(observerReady)
		<-writerEntered

		postStartValidations := 1
		observationErr := observe()
		if observationErr != nil {
			result <- observationResult{
				writeErr:             <-writeDone,
				observationErr:       observationErr,
				postStartValidations: postStartValidations,
			}
			return
		}

		for postStartValidations < cacheReadsPerRound {
			select {
			case writeErr := <-writeDone:
				result <- observationResult{
					writeErr:             writeErr,
					postStartValidations: postStartValidations,
				}
				return
			default:
			}

			observationErr = observe()
			postStartValidations++
			if observationErr != nil {
				result <- observationResult{
					writeErr:             <-writeDone,
					observationErr:       observationErr,
					postStartValidations: postStartValidations,
				}
				return
			}
		}

		select {
		case writeErr := <-writeDone:
			result <- observationResult{
				writeErr:             writeErr,
				postStartValidations: postStartValidations,
			}
		default:
			result <- observationResult{
				writeErr: <-writeDone,
				observationErr: fmt.Errorf(
					"writer did not complete within %d post-start validations",
					cacheReadsPerRound,
				),
				postStartValidations: postStartValidations,
			}
		}
	}()

	if initialErr := <-initialValidation; initialErr != nil {
		t.Fatalf("initial cache observation before write start: %v", initialErr)
	}

	go func() {
		<-observerReady
		close(writerEntered)
		writeDone <- write()
	}()

	observation := <-result
	if observation.postStartValidations <= 0 ||
		observation.postStartValidations > cacheReadsPerRound {
		t.Fatalf(
			"post-start cache validations = %d, want within [1, %d]",
			observation.postStartValidations,
			cacheReadsPerRound,
		)
	}
	if observation.writeErr != nil {
		t.Fatalf("production-path cache write error = %v", observation.writeErr)
	}
	if observation.observationErr != nil {
		t.Fatal(observation.observationErr)
	}
}
