package httpapi

import (
	"github.com/gin-gonic/gin"
)

func (s *Server) archiveTask(c *gin.Context, archived bool) {
	t, ok := s.ownedTask(c)
	if !ok {
		return
	}
	if err := s.Service.ArchiveTask(c, user(c).ID, t.ID, archived); err != nil {
		fail(c, 409, err)
		return
	}
	t, err := s.Service.Store.Task(c, t.ID)
	if err != nil {
		fail(c, 500, err)
		return
	}
	c.JSON(200, t)
}

func (s *Server) taskInputs(c *gin.Context) {
	t, ok := s.ownedTask(c)
	if !ok {
		return
	}
	assets, err := s.Service.Store.TaskAssets(c, t.Owner, t.Spec.AssetIDs)
	if err != nil {
		fail(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"task_id": t.ID, "spec": t.Spec, "assets": assets})
}
