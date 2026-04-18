package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHandlerAppTemplatesList_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/missing/templates/apps", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppTemplatesList(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestHandlerAppTemplateContent_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/missing/templates/apps/shared/nginx/content", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "templateName": "shared/nginx"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppTemplateContent(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestHandlerAppTemplateStructureGuide_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/missing/templates/apps/structure-guide", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppTemplateStructureGuide(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestTemplateStructureGuideReadErrorStatus(t *testing.T) {
	if got := templateStructureGuideReadErrorStatus(os.ErrNotExist); got != http.StatusNotFound {
		t.Fatalf("expected 404 for os.ErrNotExist, got %d", got)
	}
	if got := templateStructureGuideReadErrorStatus(errors.New("boom")); got != http.StatusInternalServerError {
		t.Fatalf("expected 500 for generic read error, got %d", got)
	}
}

func TestHandlerAppTemplateContentSave_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/templates/apps/local/t1/content/actions/save", strings.NewReader(`{"content":"x"}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "templateName": "local/t1"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppTemplateContentSave(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestHandlerAppTemplateContentDelete_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/templates/apps/local/t1/content/actions/delete", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "templateName": "local/t1"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppTemplateContentDelete(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestHandlerAppTemplateContentCreateLocalCopy_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/templates/apps/shared/nginx/content/actions/create-local-copy", strings.NewReader(`{"localTemplateName":"local/nginx"}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "templateName": "shared/nginx"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppTemplateContentCreateLocalCopy(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestHandlerAppResetFromTemplatePreview_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/apps/app1/settings/actions/reset-from-template/preview", strings.NewReader(`{"template":"shared/nginx"}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing", "appName": "app1"})
	w := httptest.NewRecorder()

	repman.handlerMuxAppResetFromTemplatePreview(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}
