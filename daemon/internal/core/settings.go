package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"

	"wisp/daemon/internal/api"
)

// settingDefaults are returned for keys never set. Values have the shapes
// encoding/json decodes to (bool, float64).
var settingDefaults = map[string]any{
	api.SettingNotifications: true,
	api.SettingNotifyPreview: true,
	api.SettingSendReceipts:  true,
	api.SettingMediaCacheMB:  float64(1024),
}

// loadSettings returns the cached settings (defaults merged with the
// store), loading them on first use. Caller holds setMu.
func (c *Core) loadSettings(ctx context.Context) map[string]any {
	if c.settings != nil {
		return c.settings
	}
	s := maps.Clone(settingDefaults)
	raw, err := c.store.AllSettings(ctx)
	if err != nil {
		c.log.Warn("core: loading settings failed; using defaults", "err", err)
		return s // not cached: retry next time
	}
	for k, js := range raw {
		var v any
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			c.log.Warn("core: bad setting value", "key", k, "err", err)
			continue
		}
		s[k] = v
	}
	c.settings = s
	return s
}

func (c *Core) setting(ctx context.Context, key string) any {
	c.setMu.Lock()
	defer c.setMu.Unlock()
	return c.loadSettings(ctx)[key]
}

func (c *Core) settingBool(ctx context.Context, key string) bool {
	if v, ok := c.setting(ctx, key).(bool); ok {
		return v
	}
	v, _ := settingDefaults[key].(bool)
	return v
}

func (c *Core) cacheBytes(ctx context.Context) int64 {
	mb, ok := c.setting(ctx, api.SettingMediaCacheMB).(float64)
	if !ok {
		mb = settingDefaults[api.SettingMediaCacheMB].(float64)
	}
	return int64(mb) << 20
}

func (c *Core) SettingsGet(ctx context.Context) (*api.SettingsGetResult, error) {
	c.setMu.Lock()
	defer c.setMu.Unlock()
	s := maps.Clone(c.loadSettings(ctx))
	delete(s, settingResynced)
	return &api.SettingsGetResult{Settings: s}, nil
}

func (c *Core) SettingsSet(ctx context.Context, p api.SettingsSetParams) error {
	if p.Key == "" {
		return api.InvalidParams("key is required")
	}
	val := p.Value
	switch def := settingDefaults[p.Key].(type) {
	case bool:
		if _, ok := val.(bool); !ok {
			return api.InvalidParams("%s must be a boolean", p.Key)
		}
	case float64:
		f, ok := val.(float64)
		if !ok || f < 0 {
			return api.InvalidParams("%s must be a non-negative number (default %v)", p.Key, def)
		}
		val = math.Trunc(f)
	}
	if err := c.saveSetting(ctx, p.Key, val); err != nil {
		return err
	}
	if p.Key == api.SettingMediaCacheMB {
		c.media.SetCap(c.cacheBytes(ctx))
	}
	return nil
}

func (c *Core) saveSetting(ctx context.Context, key string, val any) error {
	js, err := json.Marshal(val)
	if err != nil {
		return api.InvalidParams("%v", err)
	}
	if err := c.store.SetSetting(ctx, key, string(js)); err != nil {
		return fmt.Errorf("save setting: %w", err)
	}
	var dec any // same shape as a fresh load
	_ = json.Unmarshal(js, &dec)
	c.setMu.Lock()
	c.loadSettings(ctx)[key] = dec
	c.setMu.Unlock()
	return nil
}
