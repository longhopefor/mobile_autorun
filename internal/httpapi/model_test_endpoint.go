package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"mobile_autorun/internal/model"
	"time"
)

func (s *Server) probe(c *gin.Context) {
	var b struct {
		DeviceID string `json:"device_id"`
	}
	if !bind(c, &b) {
		return
	}
	owner := user(c).ID
	p, e := s.Service.Store.Profile(c, owner, c.Param("id"))
	if e != nil {
		fail(c, 404, errors.New("model_not_found"))
		return
	}
	if e = s.Service.Store.OwnPeer(c, owner, b.DeviceID, "device"); e != nil {
		fail(c, 404, errors.New("device_not_found"))
		return
	}
	ctx, stop := context.WithTimeout(c, 120*time.Second)
	defer stop()
	o, e := s.Service.Driver.Observe(ctx, b.DeviceID)
	if e == nil {
		e = s.Service.Store.SaveObservation(ctx, owner, "", &o)
	}
	if e != nil {
		fail(c, 409, e)
		return
	}
	deadline, _ := ctx.Deadline()
	q := model.Request{ID: uuid.NewString(), TaskID: "probe", Profile: p, Goal: "只根据实际截图提出下一步，不执行动作。识别屏幕内容并返回一个 phone_action、finish_task 或 request_takeover 决策。", ObservationID: o.ID, Image: base64.StdEncoding.EncodeToString(o.Image), State: o.State, Packages: []string{"com.xingin.xhs", "com.android.settings"}, Deadline: deadline.Format(time.RFC3339Nano)}
	var provider model.Provider = &model.API{}
	if p.Provider != "api" {
		provider = &model.Remote{Gateway: s.Service.Models}
	}
	started := time.Now()
	r, e := provider.Next(ctx, q)
	if e != nil {
		fail(c, 409, e)
		return
	}
	c.JSON(200, gin.H{"result": r, "observation": o, "executed": false, "duration_ms": time.Since(started).Milliseconds(), "provider": p.Provider, "model": p.Model})
}
