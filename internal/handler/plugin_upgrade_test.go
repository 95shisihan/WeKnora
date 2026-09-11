package handler

import (
	"path/filepath"
	"testing"

	pluginhost "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUpgradeRenamesOnlyMatchingDefaultDataSources(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "upgrade.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.DataSource{}))
	for _, ds := range []types.DataSource{
		{ID: "default", TenantID: 1, Type: "example", Name: "Old name", Config: types.JSON(`{"url":"https://example.com"}`), LastSyncCursor: types.JSON(`{"cursor":42}`)},
		{ID: "custom", TenantID: 1, Type: "example", Name: "My documents"},
		{ID: "other-tenant", TenantID: 2, Type: "example", Name: "Old name"},
		{ID: "other-plugin", TenantID: 1, Type: "another", Name: "Old name"},
	} {
		require.NoError(t, db.Create(&ds).Error)
	}
	before := &pluginhost.Manifest{Metadata: pluginhost.Metadata{Name: "Old name"}, Spec: pluginhost.Spec{ExtensionPoints: []pluginhost.ExtensionPoint{{Type: pluginhost.ExtensionDatasource, ID: "example"}}}}
	after := &pluginhost.Manifest{Metadata: pluginhost.Metadata{Name: "Plugin name"}, Spec: pluginhost.Spec{ExtensionPoints: []pluginhost.ExtensionPoint{{Type: pluginhost.ExtensionDatasource, ID: "example", Name: "New name"}}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		count, err := renamePluginDataSources(tx, before, after)
		require.EqualValues(t, 2, count)
		return err
	}))
	var rows []types.DataSource
	require.NoError(t, db.Find(&rows).Error)
	for _, row := range rows {
		switch row.ID {
		case "default":
			require.Equal(t, "New name", row.Name)
			require.JSONEq(t, `{"url":"https://example.com"}`, string(row.Config))
			require.JSONEq(t, `{"cursor":42}`, string(row.LastSyncCursor))
		case "other-tenant":
			require.Equal(t, "New name", row.Name)
		case "custom":
			require.Equal(t, "My documents", row.Name)
		case "other-plugin":
			require.Equal(t, "Old name", row.Name)
		}
	}
}
