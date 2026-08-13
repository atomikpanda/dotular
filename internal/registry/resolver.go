package registry

import (
	"context"
	"fmt"

	"github.com/atomikpanda/dotular/internal/config"
	tmpl "github.com/atomikpanda/dotular/internal/template"
	"github.com/atomikpanda/dotular/internal/ui"
)

// Resolve processes every module in cfg. Modules with a From field are
// fetched from the registry, parameterised, and have their overrides merged.
// The returned Config has no From fields — all modules are fully materialised.
//
// configPath is the path to dotular.yaml and is used to locate the lockfile.
// When noCache is true, all registry modules are re-fetched from the network.
func Resolve(ctx context.Context, cfg config.Config, configPath string, noCache bool, u *ui.UI) (config.Config, error) {
	hasRegistryModule := false
	for _, mod := range cfg.Modules {
		if mod.IsRegistry() {
			hasRegistryModule = true
			break
		}
	}
	if !hasRegistryModule {
		return config.Config{
			Age:     cfg.Age,
			Modules: append([]config.Module(nil), cfg.Modules...),
		}, nil
	}

	lockPath := LockPath(configPath)
	release, err := AcquireWriterLock(lockPath, configPath)
	if err != nil {
		return config.Config{}, err
	}
	defer func() {
		if err := release(); err != nil {
			u.Warn(fmt.Sprintf("could not release registry writer lock: %v", err))
		}
	}()

	lock, err := LoadLock(lockPath)
	if err != nil {
		return config.Config{}, fmt.Errorf("load lockfile: %w", err)
	}

	result := config.Config{Age: cfg.Age}
	lockDirty := false
	requiresPinPersistence := false

	for _, mod := range cfg.Modules {
		if !mod.IsRegistry() {
			result.Modules = append(result.Modules, mod)
			continue
		}
		_, hadPin := lock.Registry[mod.From]

		// Never Repin: applying a config must use what was approved, so an
		// upstream change surfaces as an error rather than a silent adoption.
		remote, trust, err := Fetch(ctx, mod.From, lock, FetchOptions{NoCache: noCache}, u)
		if err != nil {
			return config.Config{}, err
		}
		if !hadPin {
			requiresPinPersistence = true
		}

		switch trust {
		case External:
			u.Warn(fmt.Sprintf("[external] %s", mod.From))
		}

		params := resolveParams(remote.Params, mod.With)

		renderedItems, err := renderItems(remote.Items, params)
		if err != nil {
			return config.Config{}, fmt.Errorf("render %s: %w", mod.From, err)
		}

		mergedItems := mergeOverrides(renderedItems, mod.Override)

		name := remote.Name
		if mod.Name != "" {
			name = mod.Name
		}

		result.Modules = append(result.Modules, config.Module{
			Name:        name,
			Items:       mergedItems,
			OnlyTags:    mod.OnlyTags,
			ExcludeTags: mod.ExcludeTags,
			Hooks:       mod.Hooks,
		})
		lockDirty = true
	}

	if lockDirty {
		if err := SaveLock(lockPath, lock); err != nil {
			if requiresPinPersistence {
				return config.Config{}, fmt.Errorf("save first registry pin: %w", err)
			}
			u.Warn(fmt.Sprintf("could not save lockfile: %v", err))
		}
	}

	return result, nil
}

// resolveParams merges user-supplied with values over the module's defaults.
func resolveParams(defs map[string]Param, with map[string]any) map[string]any {
	params := make(map[string]any, len(defs))
	for k, def := range defs {
		params[k] = def.Default
	}
	for k, v := range with {
		params[k] = v
	}
	return params
}

// renderItems renders Go template expressions in every item's string fields.
func renderItems(items []config.Item, params map[string]any) ([]config.Item, error) {
	rendered := make([]config.Item, 0, len(items))
	for _, item := range items {
		r, err := tmpl.RenderItem(item, params)
		if err != nil {
			return nil, err
		}
		rendered = append(rendered, r)
	}
	return rendered, nil
}

// mergeOverrides replaces items in base with matching overrides (matched by
// type + primary value). Overrides that match nothing are appended.
func mergeOverrides(base, overrides []config.Item) []config.Item {
	if len(overrides) == 0 {
		return base
	}

	type key struct{ typ, val string }
	overrideMap := make(map[key]config.Item, len(overrides))
	for _, ov := range overrides {
		overrideMap[key{ov.Type(), ov.PrimaryValue()}] = ov
	}

	result := make([]config.Item, len(base))
	replaced := make(map[key]bool)

	for i, item := range base {
		k := key{item.Type(), item.PrimaryValue()}
		if ov, ok := overrideMap[k]; ok {
			result[i] = ov
			replaced[k] = true
		} else {
			result[i] = item
		}
	}

	// Append overrides that didn't match any base item.
	for _, ov := range overrides {
		k := key{ov.Type(), ov.PrimaryValue()}
		if !replaced[k] {
			result = append(result, ov)
		}
	}

	return result
}
