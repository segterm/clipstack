package proto

import "encoding/json"

type MsgType = string

const (
	MsgList   MsgType = "list"
	MsgSearch MsgType = "search"
	MsgPin    MsgType = "pin"
	MsgUnpin  MsgType = "unpin"
	MsgDelete MsgType = "delete"
	MsgCopy   MsgType = "copy"
	MsgNote   MsgType = "note"
	MsgHide   MsgType = "hide"
	MsgUnhide MsgType = "unhide"
	MsgResp   MsgType = "resp"
	MsgErr    MsgType = "err"
)

type Request struct {
	Type   MsgType `json:"type"`
	ID     int64   `json:"id,omitempty"`
	Query  string  `json:"query,omitempty"`
	Note   string  `json:"note,omitempty"`
	Pinned bool    `json:"pinned,omitempty"`
	Limit  int     `json:"limit,omitempty"`
	Offset int     `json:"offset,omitempty"`
}

type Item struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	Note      string `json:"note,omitempty"`
	Pinned    bool   `json:"pinned"`
	Hidden    bool   `json:"hidden,omitempty"`
	CreatedAt string `json:"created_at"`
}

type Response struct {
	Type  MsgType `json:"type"`
	Items []Item  `json:"items,omitempty"`
	Error string  `json:"error,omitempty"`
}

func Encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
