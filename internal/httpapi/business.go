package httpapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"github.com/gin-gonic/gin"
	"mobile_autorun/internal/app"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/store"
	"strings"
	"time"
)

func spreadsheetText(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}
func (s *Server) businessRoutes(a *gin.RouterGroup) {
	a.GET("/business/records", func(c *gin.Context) {
		v, e := s.Service.Store.BusinessEntries(c, user(c).ID, c.Query("kind"), c.Query("task_id"))
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.GET("/business/leads.csv", func(c *gin.Context) {
		v, e := s.Service.Store.ExportBusinessLeads(c, user(c).ID, c.Query("task_id"))
		if e != nil {
			fail(c, 500, e)
			return
		}
		var b bytes.Buffer
		b.WriteString("\xef\xbb\xbf")
		w := csv.NewWriter(&b)
		_ = w.Write([]string{"平台", "账号", "客户", "联系方式", "意向判断", "判断依据", "需求原文", "来源", "任务", "页面证据", "更新时间"})
		for _, l := range v {
			row := []string{l.Platform, l.Account, l.Record.Name, l.Record.Contact, l.Record.Intent, l.Record.Reason, l.Record.Evidence, l.Record.Source, l.TaskID, l.ObservationID, l.UpdatedAt.Format(time.RFC3339)}
			for i := range row {
				row[i] = spreadsheetText(row[i])
			}
			_ = w.Write(row)
		}
		w.Flush()
		if e = w.Error(); e != nil {
			fail(c, 500, e)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="leads.csv"`)
		c.Header("Cache-Control", "private, no-store")
		c.Data(200, "text/csv; charset=utf-8", b.Bytes())
	})
	a.GET("/business/outreach", func(c *gin.Context) {
		rows, e := s.Service.Store.Pool.Query(c, "SELECT id,task_id,platform,account,recipient,data,status,observation_id,created_at FROM business_outreach WHERE owner=$1 ORDER BY created_at DESC LIMIT 500", user(c).ID)
		if e != nil {
			fail(c, 500, e)
			return
		}
		defer rows.Close()
		out := []gin.H{}
		for rows.Next() {
			var id, task, p, acct, n, status, obs string
			var record domain.BusinessRecord
			var created time.Time
			if e = rows.Scan(&id, &task, &p, &acct, &n, &record, &status, &obs, &created); e != nil {
				fail(c, 500, e)
				return
			}
			out = append(out, gin.H{"id": id, "task_id": task, "platform": p, "account": acct, "recipient": n, "record": record, "status": status, "observation_id": obs, "created_at": created})
		}
		if e = rows.Err(); e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, out)
	})
	a.GET("/materials", func(c *gin.Context) {
		v, e := s.Service.Materials(c, user(c).ID)
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.POST("/materials", func(c *gin.Context) {
		var spec app.MaterialSpec
		if !bind(c, &spec) {
			return
		}
		v, e := s.Service.CreateMaterial(c, user(c).ID, c.GetHeader("Idempotency-Key"), spec)
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.JSON(201, v)
	})
	a.GET("/materials/:id", func(c *gin.Context) {
		v, e := s.Service.Material(c, user(c).ID, c.Param("id"))
		if e != nil {
			fail(c, 404, errors.New("material_not_found"))
			return
		}
		c.JSON(200, v)
	})
	a.POST("/materials/:id/generate", func(c *gin.Context) {
		var payload struct {
			Content *domain.BusinessRecord `json:"content"`
		}
		if !bind(c, &payload) {
			return
		}
		ctx, done := context.WithTimeout(c.Request.Context(), 8*time.Minute)
		defer done()
		v, e := s.Service.GenerateMaterial(ctx, user(c).ID, c.Param("id"), payload.Content)
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.JSON(200, v)
	})
	a.GET("/materials/:id/export", func(c *gin.Context) {
		v, e := s.Service.MaterialZIP(c, user(c).ID, c.Param("id"))
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="product-materials.zip"`)
		c.Header("Cache-Control", "private, no-store")
		c.Data(200, "application/zip", v)
	})
	a.POST("/materials/:id/draft", func(c *gin.Context) {
		var in struct {
			DeviceID  string `json:"device_id"`
			Account   string `json:"account"`
			Confirmed bool   `json:"confirmed"`
		}
		if !bind(c, &in) {
			return
		}
		if !in.Confirmed {
			fail(c, 400, errors.New("confirm_product_content_required"))
			return
		}
		w, e := s.Service.Material(c, user(c).ID, c.Param("id"))
		if e != nil || w.Status != "ready" {
			fail(c, 400, errors.New("material_not_ready"))
			return
		}
		ids := []string{}
		for _, asset := range w.Result.Assets {
			if strings.HasPrefix(asset.Name, "xhs-") {
				ids = append(ids, asset.ID)
			}
		}
		task, e := s.Service.Create(c, user(c).ID, "material-draft:"+w.ID+":"+store.Hash(in.DeviceID+"\x00"+in.Account+"\x00"+strings.Join(ids, ",")), domain.TaskSpec{Template: "xhs_image", Mode: "draft", DeviceID: in.DeviceID, ModelProfileID: w.Spec.ModelProfileID, Account: in.Account, Title: w.Result.Content.Title, Body: w.Result.Content.Body, AssetIDs: ids, MaxIterations: 60})
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.JSON(201, task)
	})
	a.GET("/campaigns", func(c *gin.Context) {
		v, e := s.Service.Campaigns(c, user(c).ID)
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.POST("/campaigns", func(c *gin.Context) {
		var in app.Campaign
		if !bind(c, &in) {
			return
		}
		v, e := s.Service.CreateCampaign(c, user(c).ID, in)
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.JSON(201, v)
	})
	a.PATCH("/campaigns/:id", func(c *gin.Context) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if !bind(c, &in) {
			return
		}
		if e := s.Service.SetCampaign(c, user(c).ID, c.Param("id"), in.Enabled); e != nil {
			fail(c, 404, e)
			return
		}
		c.Status(204)
	})
}
