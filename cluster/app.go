package cluster

type AppList []App

type App interface {
	SetReprovCookie() error
}
