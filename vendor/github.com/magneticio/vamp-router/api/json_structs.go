package api

type UpdateWeight struct {
	Weight int `json:"weight" binding:"required"`
}
