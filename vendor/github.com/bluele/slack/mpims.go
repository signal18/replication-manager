package slack

import (
	"encoding/json"
	"errors"
)

// API mpim.list: Lists multiparty direct message channels for the calling user.
func (sl *Slack) MpImList() ([]*MpIm, error) {
	uv := sl.urlValues()
	body, err := sl.GetRequest(mpimListApiEndpoint, uv)
	if err != nil {
		return nil, err
	}
	res := new(MpImListAPIResponse)
	err = json.Unmarshal(body, res)
	if err != nil {
		return nil, err
	}
	if !res.Ok {
		return nil, errors.New(res.Error)
	}
	return res.MpIms()
}

// slack mpim type
type MpIm struct {
	Id         string          `json:"id"`
	Name       string          `json:"name"`
	Created    int64           `json:"created"`
	Creator    string          `json:"creator"`
	IsArchived bool            `json:"is_archived"`
	IsMpim     bool            `json:"is_mpim"`
	Members    []string        `json:"members"`
	RawTopic   json.RawMessage `json:"topic"`
	RawPurpose json.RawMessage `json:"purpose"`
}

// response type for `im.list` api
type MpImListAPIResponse struct {
	BaseAPIResponse
	RawMpIms json.RawMessage `json:"groups"`
}

// MpIms returns a slice of mpim object from `mpim.list` api.
func (res *MpImListAPIResponse) MpIms() ([]*MpIm, error) {
	var mpim []*MpIm
	err := json.Unmarshal(res.RawMpIms, &mpim)
	if err != nil {
		return nil, err
	}
	return mpim, nil
}

// FindMpIm returns a mpim object that satisfy conditions specified.
func (sl *Slack) FindMpIm(cb func(*MpIm) bool) (*MpIm, error) {
	mpims, err := sl.MpImList()
	if err != nil {
		return nil, err
	}
	for _, mpim := range mpims {
		if cb(mpim) {
			return mpim, nil
		}
	}
	return nil, errors.New("No such mpim.")
}
