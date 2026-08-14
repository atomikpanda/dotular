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
	cacheReadsPerRound      = 24
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

	ready := make(chan struct{})
	start := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		close(ready)
		<-start

		for range cacheReadsPerRound {
			data, err := os.ReadFile(path)
			if validationErr := validate(data, err); validationErr != nil {
				result <- validationErr
				return
			}
		}
		result <- nil
	}()

	<-ready
	close(start)

	writeErr := write()
	observationErr := <-result

	if writeErr != nil {
		t.Fatalf("production-path cache write error = %v", writeErr)
	}
	if observationErr != nil {
		t.Fatal(observationErr)
	}
}
