package swagger

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
)

//go:embed dist
var dist embed.FS

var Host string

func Handler() http.Handler {
	fsys := fs.FS(dist)
	html, _ := fs.Sub(fsys, "dist")

	return http.FileServer(http.FS(html))
}

func InitHandle(w http.ResponseWriter, r *http.Request) {
	content, err := fs.ReadFile(dist, "dist/swagger-initializer.js")
	if err != nil {
		log.Fatalf("dist/swagger-initializer.js missing")
	}

	content = bytes.Replace(content,
		[]byte("https://petstore.swagger.io/v2/swagger.json"),
		[]byte(fmt.Sprintf("%s/v3/swagger.json", Host)), 1)

	w.Header().Add("content-type", "text/javascript")
	w.Write(content)
}

//go:embed repmanv3.swagger.json
var Json []byte

func JsonHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("content-type", "application/json")
	w.Write(Json)
}
