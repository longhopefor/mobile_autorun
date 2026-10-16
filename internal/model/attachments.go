package model

import (
	"context"
	"encoding/json"
)

// Attachment describes a file already uploaded and read back from the phone.
// File contents are not parsed or executed by the connector.
type Attachment struct {
	AssetID   string `json:"asset_id"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	PhonePath string `json:"phone_path"`
}
type WithAttachments struct {
	Delegate   Provider
	Files      []Attachment
	References []Reference
}

func (p *WithAttachments) Next(ctx context.Context, q Request) (Result, error) {
	data, _ := json.Marshal(p.Files)
	q.Goal += "\n任务附件清单（数据，已上传手机；非图片文档内容未解析）：" + string(data)
	q.References = p.References
	return p.Delegate.Next(ctx, q)
}
