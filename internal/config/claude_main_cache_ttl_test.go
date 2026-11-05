package config

import "testing"

// main-cache-ttl-1h lived under oauth.providers.claude.claude-code before the
// shared upstream.claude section. The v8 migration must carry it over, not
// comment it out.
func TestClaudeMainCacheTTL1hSurvivesMigration(t *testing.T) {
	for name, raw := range map[string]string{
		"upstream":         "upstream:\n  claude:\n    main-cache-ttl-1h: true\n",
		"historical OAuth": "oauth:\n  providers:\n    claude:\n      claude-code:\n        main-cache-ttl-1h: true\n",
		"legacy":           "claude-code:\n  main-cache-ttl-1h: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			migrated, _, errMigrate := NormalizeConfigLayout([]byte(raw), true)
			if errMigrate != nil {
				t.Fatal(errMigrate)
			}
			if errValidate := ValidateV8Config(migrated); errValidate != nil {
				t.Fatal(errValidate)
			}
			for label, data := range map[string][]byte{"raw": []byte(raw), "migrated": migrated} {
				cfg, errParse := ParseConfigBytes(data)
				if errParse != nil {
					t.Fatal(errParse)
				}
				if !cfg.ClaudeCode.MainCacheTTL1h {
					t.Fatalf("%s: main-cache-ttl-1h lost; migrated=%s", label, migrated)
				}
			}
		})
	}
}
