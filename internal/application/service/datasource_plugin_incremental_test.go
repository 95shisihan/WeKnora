package service

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"path/filepath"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/plugin/datasourcegrpc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const incrementalAcceptanceConnectorType = "acceptance-incremental-plugin"

// incrementalAcceptancePlugin is a real gRPC datasource plugin used at the
// application acceptance boundary. The first cursor produces two files and
// the next cursor produces only the single file changed at the source.
type incrementalAcceptancePlugin struct {
	pluginproto.UnimplementedDatasourcePluginServer
	starts []*types.SyncCursor
}

func (*incrementalAcceptancePlugin) GetInfo(
	context.Context, *pluginproto.GetInfoRequest,
) (*pluginproto.GetInfoResponse, error) {
	return &pluginproto.GetInfoResponse{
		Id: "dev.example.incremental-acceptance", Version: "1.0.0",
		ProtocolVersion: "v1", ConnectorType: incrementalAcceptanceConnectorType,
	}, nil
}

func (p *incrementalAcceptancePlugin) Fetch(
	request *pluginproto.FetchRequest,
	stream grpc.ServerStreamingServer[pluginproto.FetchEvent],
) error {
	var cursor *types.SyncCursor
	if len(request.GetCursorJson()) > 0 {
		cursor = &types.SyncCursor{}
		if err := json.Unmarshal(request.GetCursorJson(), cursor); err != nil {
			return err
		}
	}
	p.starts = append(p.starts, cursor)
	if cursor == nil {
		for _, item := range []*pluginproto.FetchedItem{
			{ExternalId: "one.txt", FileName: "one.txt", Content: []byte("one")},
			{ExternalId: "two.txt", FileName: "two.txt", Content: []byte("two")},
		} {
			if err := stream.Send(&pluginproto.FetchEvent{
				Payload: &pluginproto.FetchEvent_Item{Item: item},
			}); err != nil {
				return err
			}
		}
		return sendAcceptanceCursor(stream, 1)
	}
	if err := stream.Send(&pluginproto.FetchEvent{
		Payload: &pluginproto.FetchEvent_Item{Item: &pluginproto.FetchedItem{
			ExternalId: "two.txt", FileName: "two.txt", Content: []byte("two changed"),
		}},
	}); err != nil {
		return err
	}
	return sendAcceptanceCursor(stream, 2)
}

func acceptanceCursor(revision int) *types.SyncCursor {
	return &types.SyncCursor{
		LastSyncTime: time.Now().UTC(),
		ConnectorCursor: map[string]interface{}{
			"revision": float64(revision),
		},
	}
}

func sendAcceptanceCursor(
	stream grpc.ServerStreamingServer[pluginproto.FetchEvent], revision int,
) error {
	raw, err := json.Marshal(acceptanceCursor(revision))
	if err != nil {
		return err
	}
	return stream.Send(&pluginproto.FetchEvent{
		Payload: &pluginproto.FetchEvent_FinalCursorJson{FinalCursorJson: raw},
	})
}

func startIncrementalAcceptancePlugin(
	t *testing.T,
) (*incrementalAcceptancePlugin, *datasourcegrpc.Connector) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	plugin := &incrementalAcceptancePlugin{}
	server := grpc.NewServer()
	pluginproto.RegisterDatasourcePluginServer(server, plugin)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connector, err := datasourcegrpc.Dial(
		ctx, listener.Addr().String(), "dev.example.incremental-acceptance", "1.0.0",
		incrementalAcceptanceConnectorType,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })
	return plugin, connector
}

type incrementalAcceptanceKnowledgeService struct {
	interfaces.KnowledgeService
	actual  *knowledgeService
	repo    interfaces.KnowledgeRepository
	deleted []string
}

func (s *incrementalAcceptanceKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *incrementalAcceptanceKnowledgeService) DeleteKnowledge(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *incrementalAcceptanceKnowledgeService) CreateKnowledgeFromFile(
	ctx context.Context,
	kbID string,
	file *multipart.FileHeader,
	metadata map[string]string,
	enableMultimodel *bool,
	customFileName string,
	tagIDs []string,
	channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	return s.actual.CreateKnowledgeFromFile(
		ctx, kbID, file, metadata, enableMultimodel, customFileName,
		tagIDs, channel, processOverrides,
	)
}

func runIncrementalAcceptanceSync(
	t *testing.T,
	svc *DataSourceService,
	ds *types.DataSource,
	repo *processSyncSyncLogRepo,
	logID string,
) *types.SyncLog {
	t.Helper()
	repo.logs[logID] = &types.SyncLog{
		ID: logID, DataSourceID: ds.ID, TenantID: ds.TenantID,
		Status: types.SyncLogStatusRunning, StartedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID, TenantID: ds.TenantID, SyncLogID: logID,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(
		context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload),
	))
	return repo.logs[logID]
}

// TestPluginIncrementalSyncOnlyReprocessesChangedFile covers the host-side
// acceptance boundary after plugin Fetch: the second cursor emits one changed
// file, and only that file reaches CreateKnowledgeFromFile. The local-directory
// plugin's own gRPC round-trip test separately proves its SHA-256 scanner emits
// exactly this one-item delta.
func TestPluginIncrementalSyncOnlyReprocessesChangedFile(t *testing.T) {
	configJSON, err := (&types.DataSourceConfig{Type: incrementalAcceptanceConnectorType}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID: "ds-plugin-incremental", TenantID: 1, KnowledgeBaseID: "kb-1",
		Name: "Incremental Plugin", Type: incrementalAcceptanceConnectorType,
		Config: configJSON, SyncMode: types.SyncModeIncremental,
		Status: types.DataSourceStatusActive,
	}
	plugin, connector := startIncrementalAcceptancePlugin(t)
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(connector))
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "plugin-sync.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{},
	))
	knowledgeRepo := apprepo.NewKnowledgeRepository(db)
	fileService := &createKnowledgeFileServiceStub{}
	parseTasks := &createKnowledgeTaskEnqueuerStub{}
	kbService := &processSyncKBService{kb: &types.KnowledgeBase{
		ID: ds.KnowledgeBaseID, TenantID: ds.TenantID,
	}}
	actualKnowledgeService := &knowledgeService{
		repo: knowledgeRepo, kbService: kbService, fileSvc: fileService, task: parseTasks,
	}
	knowledgeService := &incrementalAcceptanceKnowledgeService{
		actual: actualKnowledgeService, repo: knowledgeRepo,
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: make(map[string]*types.SyncLog)}
	svc := &DataSourceService{
		dsRepo:            newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo:       syncLogRepo,
		knowledgeService:  knowledgeService,
		kbService:         kbService,
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}

	first := runIncrementalAcceptanceSync(t, svc, ds, syncLogRepo, "sync-first")
	assert.Equal(t, 2, first.ItemsTotal)
	assert.Equal(t, 2, first.ItemsCreated)
	var storedKnowledge []types.Knowledge
	require.NoError(t, db.Order("file_name ASC").Find(&storedKnowledge).Error)
	require.Len(t, storedKnowledge, 2)
	assert.Equal(t, []string{"one.txt", "two.txt"}, []string{
		storedKnowledge[0].FileName, storedKnowledge[1].FileName,
	})
	assert.Equal(t, 2, fileService.saveCalls)
	assert.Equal(t, 2, parseTasks.calls)
	require.NotEmpty(t, ds.LastSyncCursor)
	firstTwoKnowledge, err := knowledgeRepo.FindByDataSourceExternalID(
		context.Background(), ds.TenantID, ds.KnowledgeBaseID, ds.ID, "two.txt",
	)
	require.NoError(t, err)
	require.NotNil(t, firstTwoKnowledge)
	firstTwoKnowledgeID := firstTwoKnowledge.ID
	firstTwoFileHash := firstTwoKnowledge.FileHash
	firstOneKnowledge, err := knowledgeRepo.FindByDataSourceExternalID(
		context.Background(), ds.TenantID, ds.KnowledgeBaseID, ds.ID, "one.txt",
	)
	require.NoError(t, err)
	require.NotNil(t, firstOneKnowledge)

	second := runIncrementalAcceptanceSync(t, svc, ds, syncLogRepo, "sync-second")
	assert.Equal(t, 1, second.ItemsTotal)
	assert.Equal(t, 0, second.ItemsCreated)
	assert.Equal(t, 1, second.ItemsUpdated)
	storedKnowledge = nil
	require.NoError(t, db.Order("file_name ASC").Find(&storedKnowledge).Error)
	require.Len(t, storedKnowledge, 2)
	secondTwoKnowledge, err := knowledgeRepo.FindByDataSourceExternalID(
		context.Background(), ds.TenantID, ds.KnowledgeBaseID, ds.ID, "two.txt",
	)
	require.NoError(t, err)
	require.NotNil(t, secondTwoKnowledge)
	assert.NotEqual(t, firstTwoKnowledgeID, secondTwoKnowledge.ID)
	assert.NotEqual(t, firstTwoFileHash, secondTwoKnowledge.FileHash)
	secondOneKnowledge, err := knowledgeRepo.FindByDataSourceExternalID(
		context.Background(), ds.TenantID, ds.KnowledgeBaseID, ds.ID, "one.txt",
	)
	require.NoError(t, err)
	require.NotNil(t, secondOneKnowledge)
	assert.Equal(t, firstOneKnowledge.ID, secondOneKnowledge.ID)
	assert.Equal(t, 3, fileService.saveCalls)
	assert.Equal(t, 3, parseTasks.calls)
	assert.Equal(t, []string{firstTwoKnowledgeID}, knowledgeService.deleted)
	require.Len(t, plugin.starts, 2)
	require.Nil(t, plugin.starts[0])
	require.NotNil(t, plugin.starts[1])
	assert.Equal(t, float64(1), plugin.starts[1].ConnectorCursor["revision"])
}
