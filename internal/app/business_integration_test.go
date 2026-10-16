package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"image"
	"image/png"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func savedBusinessFixture(t *testing.T) (*Service, domain.Task, domain.Observation) {
	t.Helper()
	a, owner, spec := testApp(t)
	spec.Template = "wechat_crm"
	spec.Mode = "followup"
	spec.Account = "我的账号"
	spec.Business = &domain.BusinessSpec{Targets: []string{"客户甲"}, MaxReplies: 3, MaxRecords: 10, IntervalSeconds: 15, Prompt: "回复客户"}
	task, e := a.Create(context.Background(), owner, uuid.NewString(), spec)
	if e != nil {
		t.Fatal(e)
	}
	var picture bytes.Buffer
	_ = png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 400, 800)))
	nodes := []domain.UINode{}
	for i, v := range []string{"我的账号", "微信号 wxid_owner", "客户甲", "请问多少钱", "发送"} {
		nodes = append(nodes, domain.UINode{Text: v, Visible: true, Enabled: true, Bounds: domain.Bounds{Left: 10, Top: i*60 + 10, Right: 390, Bottom: i*60 + 45}})
	}
	nodes = append(nodes, domain.UINode{Text: "这是回复", Editable: true, Visible: true, Enabled: true, Bounds: domain.Bounds{Left: 10, Top: 500, Right: 390, Bottom: 550}})
	raw, _ := json.Marshal(map[string]any{"phone_state": map[string]any{"packageName": "com.tencent.mm"}, "a11y_tree": nodes})
	o := domain.Observation{ID: uuid.NewString(), DeviceID: spec.DeviceID, Width: 400, Height: 800, Image: picture.Bytes(), State: raw, CapturedAt: time.Now()}
	if e = a.Store.SaveObservation(context.Background(), owner, task.ID, &o); e != nil {
		t.Fatal(e)
	}
	return a, task, o
}
func TestBusinessRecordsOwnershipAndUnknownDispatchNotRepeated(t *testing.T) {
	a, task, o := savedBusinessFixture(t)
	ctx := context.Background()
	ops, e := a.operations(ctx, task, &model.API{})
	if e != nil {
		t.Fatal(e)
	}
	executeCount := 0
	execute := func(context.Context, domain.Decision) (domain.Observation, error) {
		executeCount++
		return o, domain.ErrUncertain
	}
	account := domain.BusinessRecord{Kind: "account", Key: "account", Name: "我的账号"}
	lead := domain.BusinessRecord{Kind: "lead", Key: "lead", Name: "客户甲", Source: "客户甲会话", Evidence: "请问多少钱", Intent: "high", Reason: "明确问价格"}
	for _, r := range []domain.BusinessRecord{account, lead} {
		if _, e = a.handleBusiness(ctx, task, ops, domain.Decision{Tool: "record_business", Record: &r}, o, execute); e != nil {
			t.Fatal(e)
		}
	}
	entries, e := a.Store.BusinessEntries(ctx, task.Owner, "lead", task.ID)
	if e != nil || len(entries) != 1 || entries[0].AssetID != o.AssetID {
		t.Fatalf("evidence not persisted %v", e)
	}
	other, e := a.Store.BusinessEntries(ctx, "other-user", "lead", "")
	if e != nil || len(other) != 0 {
		t.Fatal("owner isolation failed")
	}
	reply := lead
	reply.Kind = "reply"
	reply.Body = "这是回复"
	_, e = a.handleBusiness(ctx, task, ops, domain.Decision{Tool: "dispatch_business", Record: &reply}, o, execute)
	if !errors.Is(e, domain.ErrUncertain) {
		t.Fatal(e)
	}
	reply.Source = "different model spelling"
	id, e := a.Store.ReserveOutreach(ctx, task, o, reply)
	if e == nil || id != "" || !strings.Contains(e.Error(), "already_reserved") {
		t.Fatalf("uncertain outgoing action repeated %s %v", id, e)
	}
	if executeCount != 1 {
		t.Fatal("dispatch count")
	}
	var status string
	if e = a.Store.Pool.QueryRow(ctx, "SELECT status FROM business_outreach WHERE task_id=$1", task.ID).Scan(&status); e != nil || status != "uncertain" {
		t.Fatal("uncertainty not persisted", e)
	}
}

func TestBusinessLeadExportIncludesContactsBeyondDisplayLimit(t *testing.T) {
	a, task, o := savedBusinessFixture(t)
	ctx := context.Background()
	_, e := a.Store.Pool.Exec(ctx, `INSERT INTO business_records(id,owner,task_id,platform,account,kind,record_key,data,observation_id)
 SELECT 'export-record-'||i,$1,$2,'wechat_crm','我的账号','lead','export-key-'||i,
 jsonb_build_object('kind','lead','key','export-key-'||i,'name','客户'||i),$3
 FROM generate_series(1,2001) AS i`, task.Owner, task.ID, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	shown, e := a.Store.BusinessEntries(ctx, task.Owner, "lead", "")
	if e != nil || len(shown) != 2000 {
		t.Fatalf("display limit: count=%d err=%v", len(shown), e)
	}
	exported, e := a.Store.ExportBusinessLeads(ctx, task.Owner, task.ID)
	if e != nil || len(exported) != 2001 {
		t.Fatalf("export dropped older contacts: count=%d err=%v", len(exported), e)
	}
	other, e := a.Store.ExportBusinessLeads(ctx, "other-user", "")
	if e != nil || len(other) != 0 {
		t.Fatal("export owner isolation failed", e)
	}
}
func TestConcurrentOutreachReservationAndLimit(t *testing.T) {
	a, task, o := savedBusinessFixture(t)
	ctx := context.Background()
	r := domain.BusinessRecord{Kind: "reply", Key: "r", Name: "客户甲", Source: "会话", Evidence: "请问多少钱", Body: "这是回复"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id, e := a.Store.ReserveOutreach(ctx, task, o, r); e == nil {
				ids <- id
			}
		}()
	}
	wg.Wait()
	close(ids)
	n := 0
	for range ids {
		n++
	}
	if n != 1 {
		t.Fatalf("reserved %d times", n)
	}
	r.Evidence = "另一条需求"
	if _, e := a.Store.ReserveOutreach(ctx, task, o, r); e == nil || e.Error() != "reply_interval_not_elapsed" {
		t.Fatalf("rate limit bypass %v", e)
	}
	task.Spec.Business.MaxReplies = 1
	if _, e := a.Store.ReserveOutreach(ctx, task, o, r); e == nil || e.Error() != "reply_limit_reached" {
		t.Fatalf("reply limit bypass %v", e)
	}
}
func TestMaterialGenerationExportAndOwnerIsolation(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx := context.Background()
	var photo bytes.Buffer
	_ = png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 100, 100)))
	asset, e := a.Store.SaveNamedAsset(ctx, owner, "product.png", "image/png", photo.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	record := domain.BusinessRecord{Kind: "material", Key: "杯子", Title: "桌面上的好杯子", Body: "真实规格：容量 300ml。", Frames: []domain.Frame{{Title: "容量 300ml", Text: "商品信息来自商家", Scene: "桌面展示"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		msgs := req["messages"].([]any)
		parts := msgs[1].(map[string]any)["content"].([]any)
		if len(parts) < 4 {
			t.Error("missing product image references")
		}
		d := domain.Decision{Tool: "record_business", Record: &record, ObservationID: ""}
		text := parts[0].(map[string]any)["text"].(string)
		i := strings.Index(text, "当前 observation_id：")
		if i < 0 {
			t.Error("no observation id")
			return
		}
		d.ObservationID = strings.Split(text[i+len("当前 observation_id："):], "\n")[0]
		b, _ := json.Marshal(d)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"name": "record_business", "arguments": string(b)}}}}}}})
	}))
	defer server.Close()
	t.Setenv("MODEL_TEST_KEY", "test-only")
	p, _ := a.Store.Profile(ctx, owner, spec.ModelProfileID)
	p.BaseURL = server.URL
	if _, e = a.Store.SaveProfile(ctx, owner, p); e != nil {
		t.Fatal(e)
	}
	ms := MaterialSpec{Name: "杯子", Facts: "容量 300ml", AssetIDs: []string{asset.ID}, ModelProfileID: p.ID}
	w, e := a.CreateMaterial(ctx, owner, "material-key", ms)
	if e != nil {
		t.Fatal(e)
	}
	same, e := a.CreateMaterial(ctx, owner, "material-key", ms)
	if e != nil || same.ID != w.ID {
		t.Fatal("material creation not idempotent", e)
	}
	w, e = a.GenerateMaterial(ctx, owner, w.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	if w.Status != "ready" || len(w.Result.Assets) < 3 {
		t.Fatal("material rendering incomplete")
	}
	b, e := a.MaterialZIP(ctx, owner, w.ID)
	if e != nil {
		t.Fatal(e)
	}
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil || len(z.File) != len(w.Result.Assets)+1 {
		t.Fatal("export missing files", e)
	}
	if _, e = a.Material(ctx, "other-user", w.ID); e == nil {
		t.Fatal("material ownership bypass")
	}
	if _, e = a.MaterialZIP(ctx, "other-user", w.ID); e == nil {
		t.Fatal("export ownership bypass")
	}
	if _, e = a.Store.Pool.Exec(ctx, "UPDATE material_workflows SET status='generating' WHERE id=$1", w.ID); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	w, e = a.Material(ctx, owner, w.ID)
	if e != nil || w.Status != "failed" {
		t.Fatal("stuck generating after restart", e)
	}
}
func TestCampaignNoOverlapAndResumeSchedule(t *testing.T) {
	a, owner, spec := testApp(t)
	ctx := context.Background()
	spec.Template = "xhs_growth"
	spec.Mode = "operate"
	spec.Account = "我的账号"
	spec.Business = &domain.BusinessSpec{MaxReplies: 1, Prompt: "根据问题回复"}
	c, e := a.CreateCampaign(ctx, owner, Campaign{Name: "账号运维", Spec: spec, IntervalMinutes: 15})
	if e != nil {
		t.Fatal(e)
	}
	if e = a.TickCampaigns(ctx); e != nil {
		t.Fatal(e)
	}
	all, _ := a.Campaigns(ctx, owner)
	if len(all) != 1 || all[0].LastTaskID == "" {
		t.Fatal("campaign not queued")
	}
	first := all[0].LastTaskID
	_, _ = a.Store.Pool.Exec(ctx, "UPDATE business_campaigns SET next_run=now()-interval '1 minute' WHERE id=$1", c.ID)
	_ = a.TickCampaigns(ctx)
	all, _ = a.Campaigns(ctx, owner)
	if all[0].LastTaskID != first {
		t.Fatal("overlapping campaign")
	}
	if e = a.SetCampaign(ctx, "other-user", c.ID, false); e == nil {
		t.Fatal("campaign ownership bypass")
	}
	if e = a.SetCampaign(ctx, owner, c.ID, false); e != nil {
		t.Fatal(e)
	}
	_, _ = a.Store.Pool.Exec(ctx, "UPDATE tasks SET status='succeeded' WHERE id=$1", first)
	_ = a.TickCampaigns(ctx)
	all, _ = a.Campaigns(ctx, owner)
	if all[0].LastTaskID != first {
		t.Fatal("disabled campaign ran")
	}
	_ = a.SetCampaign(ctx, owner, c.ID, true)
	_ = a.TickCampaigns(ctx)
	all, _ = a.Campaigns(ctx, owner)
	if all[0].LastTaskID == first {
		t.Fatal("enabled campaign did not resume")
	}
}
