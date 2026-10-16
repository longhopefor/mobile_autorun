package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"io"
	"mobile_autorun/internal/app"
	"mobile_autorun/internal/attachment"
	"mobile_autorun/internal/domain"
	"mobile_autorun/internal/mediaindex"
	"mobile_autorun/internal/store"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	Service *app.Service
	Origin  string
}

func user(c *gin.Context) store.User { return c.MustGet("user").(store.User) }
func fail(c *gin.Context, status int, e error) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": e.Error()}})
}
func bind(c *gin.Context, v any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	b, e := io.ReadAll(c.Request.Body)
	if e == nil {
		e = domain.DecodeStrict(b, v)
	}
	if e != nil {
		fail(c, 400, errors.New("invalid_request"))
		return false
	}
	return true
}
func (s *Server) Routes() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.ContextWithFallback = true
	r.Use(gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) {
		ctx, done := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer done()
		if e := s.Service.Store.Pool.Ping(ctx); e != nil {
			fail(c, 503, errors.New("database_unavailable"))
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.Use(func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if c.Request.Method != "GET" && origin != "" && origin != s.Origin && origin != "http://"+c.Request.Host && origin != "https://"+c.Request.Host {
			fail(c, 403, errors.New("origin_denied"))
			return
		}
		c.Next()
	})
	r.POST("/api/v1/auth/login", s.login)
	r.GET("/device/connect", s.deviceWS)
	r.GET("/model/connect", s.modelWS)
	r.GET("/device/media-connect", s.mediaWS)
	a := r.Group("/api/v1")
	a.Use(s.auth)
	s.businessRoutes(a)
	a.GET("/me", func(c *gin.Context) { c.JSON(200, user(c)) })
	a.POST("/auth/logout", func(c *gin.Context) {
		token, _ := c.Cookie("session")
		_, e := s.Service.Store.Pool.Exec(c, "DELETE FROM sessions WHERE token_hash=$1", store.Hash(token))
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.SetCookie("session", "", -1, "/", "", c.Request.TLS != nil, true)
		c.Status(204)
	})
	a.GET("/version", func(c *gin.Context) {
		c.JSON(200, gin.H{"version": "0.2.0", "protocol_version": 1, "minimum_client_version": "0.1.0"})
	})
	a.GET("/capabilities", func(c *gin.Context) {
		c.JSON(200, gin.H{"providers": []string{"api", "codex_cli", "workbuddy_cli"}, "device_transports": []string{"portal_reverse", "usb_reverse", "ws"}, "templates": []string{"general", "xhs_image", "wechat_crm", "xhs_growth"}, "completion_verification": "human_evidence", "desktop_usb": false, "business_records": true, "business_campaigns": true, "material_outputs": []string{"detail_png", "xhs_png", "scene_layout_png", "mp4", "configured_ai_scene", "configured_ai_video"}, "ai_media_configured": os.Getenv("MATERIAL_GENERATOR_URL") != "", "usb_tunnel_helper": true, "media_index_transports": []string{"portal_native", "usb_legacy"}})
	})
	a.GET("/devices", func(c *gin.Context) { s.peers(c, "device") })
	a.GET("/model-connectors", func(c *gin.Context) { s.peers(c, "model") })
	a.POST("/devices/pairings", func(c *gin.Context) { s.pair(c, "device") })
	a.POST("/model-connectors/pairings", func(c *gin.Context) { s.pair(c, "model") })
	a.GET("/model-profiles", func(c *gin.Context) {
		v, e := s.Service.Store.Profiles(c, user(c).ID)
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.POST("/model-profiles", func(c *gin.Context) {
		var p domain.Profile
		if !bind(c, &p) {
			return
		}
		p, e := s.Service.Store.SaveProfile(c, user(c).ID, p)
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.JSON(201, p)
	})
	a.POST("/model-profiles/:id/test", s.probe)
	a.GET("/assets", func(c *gin.Context) {
		v, e := s.Service.Store.Assets(c, user(c).ID)
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.POST("/assets", s.upload)
	a.GET("/assets/:id/content", func(c *gin.Context) {
		a, b, e := s.Service.Store.Asset(c, user(c).ID, c.Param("id"))
		if e != nil {
			fail(c, 404, errors.New("asset_not_found"))
			return
		}
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		if !attachment.IsImage(a.MIME) {
			c.Header("Content-Disposition", attachment.Disposition(a.Name))
		}
		c.Data(200, a.MIME, b)
	})
	a.GET("/task-templates", func(c *gin.Context) {
		c.JSON(200, []any{gin.H{"id": "wechat_crm", "name": "微信 AI 客服", "modes": []string{"followup", "inbox"}}, gin.H{"id": "xhs_growth", "name": "小红书获客与运维", "modes": []string{"research", "draft", "reply", "operate"}}, gin.H{"id": "general", "name": "自然语言手机任务"}, gin.H{"id": "xhs_image", "name": "小红书图文", "modes": []string{"edit", "draft", "publish"}, "package": "com.xingin.xhs", "verification": "人工核验最新截图"}})
	})
	a.GET("/tasks", func(c *gin.Context) {
		archived := c.DefaultQuery("archived", "false")
		if archived != "true" && archived != "false" {
			fail(c, 400, errors.New("invalid_archive_filter"))
			return
		}
		v, e := s.Service.Store.TasksPage(c, user(c).ID, archived == "true", c.Query("before"))
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.POST("/tasks", func(c *gin.Context) {
		var spec domain.TaskSpec
		if !bind(c, &spec) {
			return
		}
		t, e := s.Service.Create(c, user(c).ID, c.GetHeader("Idempotency-Key"), spec)
		if e != nil {
			fail(c, 400, e)
			return
		}
		c.JSON(201, t)
	})
	a.GET("/tasks/:id", func(c *gin.Context) {
		if t, ok := s.ownedTask(c); ok {
			c.JSON(200, t)
		}
	})
	a.GET("/tasks/:id/steps", func(c *gin.Context) {
		if _, ok := s.ownedTask(c); !ok {
			return
		}
		after, _ := strconv.ParseInt(c.Query("after"), 10, 64)
		v, e := s.Service.Store.Events(c, c.Param("id"), after)
		if e != nil {
			fail(c, 500, e)
			return
		}
		c.JSON(200, v)
	})
	a.GET("/tasks/:id/events", s.events)
	a.POST("/tasks/:id/cancel", func(c *gin.Context) { s.pause(c, "cancel") })
	a.POST("/tasks/:id/takeover", func(c *gin.Context) { s.pause(c, "takeover") })
	a.POST("/tasks/:id/resume", func(c *gin.Context) {
		if _, ok := s.ownedTask(c); !ok {
			return
		}
		var b struct {
			Reconciled           bool `json:"reconciled"`
			AdditionalIterations int  `json:"additional_iterations"`
		}
		if !bind(c, &b) {
			return
		}
		if e := s.Service.ResumeWithBudget(c, c.Param("id"), b.Reconciled, b.AdditionalIterations); e != nil {
			fail(c, 409, e)
			return
		}
		c.Status(202)
	})
	a.POST("/tasks/:id/verify", s.verify)
	a.POST("/tasks/:id/archive", func(c *gin.Context) { s.archiveTask(c, true) })
	a.POST("/tasks/:id/restore", func(c *gin.Context) { s.archiveTask(c, false) })
	a.GET("/tasks/:id/inputs", s.taskInputs)
	a.GET("/devices/:id/observation", s.observe)
	a.GET("/devices/:id/media-capabilities", s.mediaCapabilities)
	a.POST("/devices/:id/manual-actions", s.manual)
	// Serve the built shared frontend; API/WS paths never fall through to the SPA.
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			fail(c, 404, errors.New("not_found"))
			return
		}
		p := filepath.Join("frontend/dist", filepath.Clean("/"+c.Request.URL.Path))
		if st, e := os.Stat(p); e == nil && !st.IsDir() {
			c.File(p)
			return
		}
		if _, e := os.Stat("frontend/dist/index.html"); e == nil {
			c.File("frontend/dist/index.html")
			return
		}
		c.JSON(404, gin.H{"error": "frontend_not_built"})
	})
	return r
}
func (s *Server) auth(c *gin.Context) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if token == "" {
		token, _ = c.Cookie("session")
	}
	u, e := s.Service.Store.Auth(c, token)
	if e != nil {
		fail(c, 401, errors.New("unauthorized"))
		return
	}
	c.Set("user", u)
	c.Next()
}
func (s *Server) login(c *gin.Context) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bind(c, &b) {
		return
	}
	u, token, e := s.Service.Store.Login(c, b.Username, b.Password)
	if e != nil {
		fail(c, 401, errors.New("invalid_credentials"))
		return
	}
	c.SetCookie("session", token, 43200, "/", "", c.Request.TLS != nil, true)
	c.JSON(200, gin.H{"user": u, "token": token})
}
func (s *Server) peers(c *gin.Context, kind string) {
	v, e := s.Service.Store.Peers(c, user(c).ID, kind)
	if e != nil {
		fail(c, 500, e)
		return
	}
	for i := range v {
		if kind == "model" {
			v[i].Online = s.Service.Models.Online(v[i].ID)
		} else {
			v[i].Online = s.Service.Devices.Online(v[i].ID)
			v[i].ConnectionTransport = s.Service.Devices.Transport(v[i].ID)
			v[i].MediaOnline = s.Service.Media.Online(v[i].ID)
		}
	}
	c.JSON(200, v)
}
func (s *Server) pair(c *gin.Context, kind string) {
	var b struct {
		Name string `json:"name"`
	}
	if !bind(c, &b) {
		return
	}
	if b.Name == "" || len(b.Name) > 100 {
		fail(c, 400, errors.New("name_required"))
		return
	}
	p, token, e := s.Service.Store.Pair(c, user(c).ID, kind, b.Name)
	if e != nil {
		fail(c, 500, e)
		return
	}
	c.JSON(201, gin.H{"peer": p, "token": token, "pairing_expires_in": 900, "credential_policy": "首次连接须在15分钟内完成，成功连接续期30天；token仅展示一次"})
}
func (s *Server) deviceWS(c *gin.Context) {
	transport, e := deviceTransport(c.Query("transport"))
	if e != nil {
		fail(c, 400, e)
		return
	}
	p, e := s.Service.Store.PeerAuth(c, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "), "device")
	if e != nil {
		fail(c, 401, errors.New("invalid_device_token"))
		return
	}
	_ = s.Service.Store.PeerMeta(c, p.ID, map[string]string{"external_id": c.GetHeader("X-Device-ID"), "device_name": c.GetHeader("X-Device-Name"), "protocol_reference_revision": "cc3fa91b15847ca9f7cc9a5afdd6b8e1db7e4ec2"})
	conn, e := acceptSocket(c.Writer, c.Request)
	if e != nil {
		return
	}
	s.Service.Devices.ServeTransport(context.WithoutCancel(c.Request.Context()), p.ID, conn, transport)
}
func (s *Server) modelWS(c *gin.Context) {
	p, e := s.Service.Store.PeerAuth(c, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "), "model")
	if e != nil {
		fail(c, 401, errors.New("invalid_connector_token"))
		return
	}
	conn, e := acceptSocket(c.Writer, c.Request)
	if e != nil {
		return
	}
	s.Service.Models.Serve(context.WithoutCancel(c.Request.Context()), p.ID, conn)
}
func (s *Server) mediaWS(c *gin.Context) {
	p, e := s.Service.Store.PeerAuth(c, strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "), "device")
	if e != nil {
		fail(c, 401, errors.New("invalid_device_token"))
		return
	}
	conn, e := acceptSocket(c.Writer, c.Request)
	if e != nil {
		return
	}
	s.Service.Media.Serve(context.WithoutCancel(c.Request.Context()), p.ID, conn)
}
func (s *Server) ownedTask(c *gin.Context) (domain.Task, bool) {
	t, e := s.Service.Store.Task(c, c.Param("id"))
	if e != nil || t.Owner != user(c).ID {
		fail(c, 404, errors.New("task_not_found"))
		return t, false
	}
	return t, true
}
func (s *Server) pause(c *gin.Context, mode string) {
	if _, ok := s.ownedTask(c); !ok {
		return
	}
	if e := s.Service.Pause(c, c.Param("id"), mode); e != nil {
		fail(c, 500, e)
		return
	}
	c.Status(202)
}
func (s *Server) events(c *gin.Context) {
	if _, ok := s.ownedTask(c); !ok {
		return
	}
	after, _ := strconv.ParseInt(c.GetHeader("Last-Event-ID"), 10, 64)
	if v, e := strconv.ParseInt(c.Query("after"), 10, 64); e == nil && v > after {
		after = v
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		v, e := s.Service.Store.Events(c, c.Param("id"), after)
		if e != nil {
			return
		}
		for _, event := range v {
			b, _ := json.Marshal(event)
			if _, e = fmt.Fprintf(c.Writer, "id: %d\nevent: step\ndata: %s\n\n", event.ID, b); e != nil {
				return
			}
			after = event.ID
		}
		if len(v) == 0 {
			_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
		}
		c.Writer.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-timer.C:
		}
	}
}
func (s *Server) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, attachment.MaxBytes+(1<<20))
	f, header, e := c.Request.FormFile("file")
	if e != nil {
		fail(c, 400, errors.New("file_required_or_request_too_large"))
		return
	}
	defer f.Close()
	defer c.Request.MultipartForm.RemoveAll()
	b, e := io.ReadAll(io.LimitReader(f, attachment.MaxBytes+1))
	if e != nil {
		fail(c, 400, errors.New("file_read_failed"))
		return
	}
	name, mime, e := attachment.Inspect(header.Filename, b)
	if e != nil {
		fail(c, 400, e)
		return
	}
	a, e := s.Service.Store.SaveNamedAsset(c, user(c).ID, name, mime, b)
	if e != nil {
		fail(c, 500, e)
		return
	}
	c.JSON(201, a)
}

func (s *Server) observe(c *gin.Context) {
	id := c.Param("id")
	taskID := c.Query("task_id")
	if e := s.Service.Store.OwnPeer(c, user(c).ID, id, "device"); e != nil {
		fail(c, 404, errors.New("device_not_found"))
		return
	}
	if taskID != "" {
		task, e := s.Service.Store.Task(c, taskID)
		if e != nil || task.Owner != user(c).ID || task.Spec.DeviceID != id {
			fail(c, 404, errors.New("task_not_found"))
			return
		}
	}
	ctx, stop := context.WithTimeout(c, 30*time.Second)
	defer stop()
	o, e := s.Service.Driver.Observe(ctx, id)
	if e == nil {
		e = s.Service.Store.SaveObservation(ctx, user(c).ID, taskID, &o)
		if e == nil && taskID != "" {
			e = s.Service.Store.Event(ctx, taskID, "observation", o)
		}
	}
	if e != nil {
		fail(c, 409, e)
		return
	}
	c.JSON(200, o)
}
func (s *Server) manual(c *gin.Context) {
	id := c.Param("id")
	if e := s.Service.Store.OwnPeer(c, user(c).ID, id, "device"); e != nil {
		fail(c, 404, errors.New("device_not_found"))
		return
	}
	var b struct {
		ObservationID string        `json:"observation_id"`
		Action        domain.Action `json:"action"`
	}
	if !bind(c, &b) {
		return
	}
	if e := b.Action.Validate([]string{"com.xingin.xhs", "com.android.settings"}); e != nil {
		fail(c, 400, e)
		return
	}
	holder := uuid.NewString()
	g, e := s.Service.Store.Acquire(c, id, holder)
	if e != nil {
		fail(c, 409, e)
		return
	}
	defer s.Service.Store.Release(context.Background(), id, holder, g)
	var assetID string
	var oldState json.RawMessage
	var oldWidth, oldHeight int
	e = s.Service.Store.Pool.QueryRow(c, "SELECT asset_id,state,width,height FROM observations WHERE id=$1 AND device_id=$2 AND owner=$3", b.ObservationID, id, user(c).ID).Scan(&assetID, &oldState, &oldWidth, &oldHeight)
	if e != nil {
		fail(c, 409, errors.New("observation_required"))
		return
	}
	_, oldImage, e := s.Service.Store.Asset(c, user(c).ID, assetID)
	if e != nil {
		fail(c, 500, e)
		return
	}
	ctx, stop := context.WithTimeout(c, 30*time.Second)
	defer stop()
	o, e := s.Service.Driver.Observe(ctx, id)
	if e != nil {
		fail(c, 409, e)
		return
	}
	if b.Action.NeedsStableScreen() {
		stable, e := domain.SameObservationForAction(o, domain.Observation{Image: oldImage, State: oldState, Width: oldWidth, Height: oldHeight}, b.Action)
		if e != nil {
			fail(c, 409, e)
			return
		}
		if !stable {
			fail(c, 409, errors.New("screen_changed_refresh_required"))
			return
		}
	}
	if e = s.Service.Store.SaveObservation(ctx, user(c).ID, "", &o); e != nil {
		fail(c, 500, e)
		return
	}
	cmd, e := s.Service.Store.Prepare(ctx, "", id, holder, o.ID, g, b.Action)
	if e != nil {
		fail(c, 409, e)
		return
	}
	e = s.Service.Driver.Execute(ctx, id, b.Action, o)
	status := "reported"
	if e != nil {
		status = "uncertain"
	}
	if ce := s.Service.Store.CommandResult(context.Background(), cmd, status, ""); ce != nil {
		fail(c, 500, ce)
		return
	}
	if e != nil {
		fail(c, 409, domain.ErrUncertain)
		return
	}
	c.JSON(200, gin.H{"action_id": cmd, "status": status})
}
func (s *Server) verify(c *gin.Context) {
	t, ok := s.ownedTask(c)
	if !ok {
		return
	}
	var b struct {
		Confirmed     bool   `json:"confirmed"`
		Reconciled    bool   `json:"reconciled"`
		ObservationID string `json:"observation_id"`
		Note          string `json:"note"`
	}
	if !bind(c, &b) {
		return
	}
	if !b.Confirmed || b.Note == "" || t.Status != "waiting_human" {
		fail(c, 409, errors.New("explicit_verification_required"))
		return
	}
	var yes bool
	e := s.Service.Store.Pool.QueryRow(c, "SELECT true FROM observations WHERE id=$1 AND task_id=$2 AND owner=$3 AND captured_at=(SELECT max(captured_at) FROM observations WHERE task_id=$2)", b.ObservationID, t.ID, user(c).ID).Scan(&yes)
	if e != nil {
		fail(c, 400, errors.New("task_evidence_required"))
		return
	}
	holder := uuid.NewString()
	g, e := s.Service.Store.Acquire(c, t.Spec.DeviceID, holder)
	if e != nil {
		fail(c, 409, e)
		return
	}
	defer s.Service.Store.Release(context.Background(), t.Spec.DeviceID, holder, g)
	if e = s.Service.Store.Verify(c, t, b.ObservationID, b.Note, b.Reconciled); e != nil {
		fail(c, 409, e)
		return
	}
	c.Status(200)
}

func (s *Server) mediaCapabilities(c *gin.Context) {
	id := c.Param("id")
	if err := s.Service.Store.OwnPeer(c, user(c).ID, id, "device"); err != nil {
		fail(c, 404, errors.New("device_not_found"))
		return
	}
	ctx, stop := context.WithTimeout(c.Request.Context(), 6*time.Second)
	defer stop()
	availability, _ := (mediaindex.Client{Portal: s.Service.Devices, Legacy: s.Service.Media}).Detect(ctx, id)
	// Detection errors are represented as unavailable with a stable reason code.
	c.Header("Cache-Control", "no-store")
	c.JSON(200, availability)
}
