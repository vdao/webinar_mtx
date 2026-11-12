package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/vdao/webinar_mtx/internal/config"
	"github.com/vdao/webinar_mtx/internal/model"
	"github.com/vdao/webinar_mtx/internal/store"
	"github.com/vdao/webinar_mtx/internal/webutil"
)

// Preset prefills webhook field paths and provides a sample payload for tests.
type Preset struct {
	Key         string
	Name        string
	EmailPath   string
	ProductPath string
	Sample      string
}

// presets are just conveniences; the engine is fully generic.
var presets = []Preset{
	{
		Key: "yookassa", Name: "ЮKassa",
		EmailPath: "object.metadata.email", ProductPath: "object.metadata.product",
		Sample: `{"type":"notification","event":"payment.succeeded","object":{"id":"2b6madf7-000f-5000-8000-18d5b6asf54a","status":"succeeded","metadata":{"email":"client@example.com","product":"webinar-pro"}}}`,
	},
	{
		Key: "stripe", Name: "Stripe",
		EmailPath: "data.object.customer_email", ProductPath: "data.object.metadata.product_id",
		Sample: `{"type":"checkout.session.completed","data":{"object":{"customer_email":"client@example.com","metadata":{"product_id":"webinar-pro"}}}}`,
	},
	{
		Key: "cloudpayments", Name: "CloudPayments",
		EmailPath: "Email", ProductPath: "ProductId",
		Sample: `{"TransactionId":3592348252,"Status":"Completed","Email":"client@example.com","ProductId":"webinar-pro","Amount":100}`,
	},
	{
		Key: "prodamus", Name: "Prodamus",
		EmailPath: "customer_email", ProductPath: "product_id",
		Sample: `{"id":123456,"customer_email":"client@example.com","product_id":"webinar-pro","status":"success"}`,
	},
	{
		Key: "tinkoff", Name: "Т-Банк (Tinkoff)",
		EmailPath: "Email", ProductPath: "Products.0.Code",
		Sample: `{"TerminalId":"1234567890","OrderId":"order-42","Success":true,"Email":"client@example.com","Products":[{"Code":"webinar-pro","Price":1000}]}`,
	},
	{
		Key: "custom", Name: "Другая система",
		EmailPath: "", ProductPath: "",
		Sample: `{"email":"client@example.com","product_id":"webinar-pro"}`,
	},
}

// webhookOutcome describes what processing a payload did.
type webhookOutcome struct {
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`
	Email   string `json:"email,omitempty"`
	Webinar string `json:"webinar,omitempty"`
	Link    string `json:"link,omitempty"`
	Created bool   `json:"created"`
}

// processWebhookPayload applies one decoded payload to a webhook config.
// Shared by the live receiver and the admin test button.
func (s *Server) processWebhookPayload(r *http.Request, wh *model.Webhook, payload map[string]any) webhookOutcome {
	if !wh.IsActive {
		return webhookOutcome{OK: false, Reason: "webhook выключен"}
	}
	email, _ := webutil.GetPath(payload, wh.EmailPath)
	email = webutil.NormalizeEmail(email)
	if !webutil.ValidEmail(email) {
		return webhookOutcome{OK: false, Reason: "в payload не найден корректный email по пути " + wh.EmailPath}
	}
	product, _ := webutil.GetPath(payload, wh.ProductPath)

	webinarID := ""
	if product != "" {
		if id, err := s.Store.FindWebhookProduct(r.Context(), wh.ID, product); err == nil {
			webinarID = id
		}
	}
	if webinarID == "" && wh.DefaultWebinarID != nil {
		webinarID = *wh.DefaultWebinarID
	}
	if webinarID == "" {
		if product == "" {
			return webhookOutcome{OK: false, Reason: "у вебхука не задан путь к product и нет вебинара по умолчанию"}
		}
		return webhookOutcome{OK: false, Reason: "товар «" + product + "» не сопоставлен ни одному вебинару"}
	}
	wb, err := s.Store.WebinarByID(r.Context(), webinarID)
	if err != nil {
		return webhookOutcome{OK: false, Reason: " вебинар из маппинга не найден"}
	}

	p, created, err := s.Store.AddParticipant(r.Context(), webinarID, email, "", model.SourceWebhook)
	if err != nil {
		return webhookOutcome{OK: false, Reason: "ошибка базы данных"}
	}
	if created {
		s.Notify.NotifyRegistered(wb, p, s.participantLink(wb, p.Code))
	}
	return webhookOutcome{OK: true, Email: email, Webinar: wb.Title, Created: created, Link: linkFor(s, wb, p)}
}

// linkFor returns the personal room link (empty for duplicates).
func linkFor(s *Server, wb *model.Webinar, p *model.Participant) string {
	if p.Code == "" {
		return ""
	}
	return s.participantLink(wb, p.Code)
}

// decodePayload parses a JSON or form-urlencoded webhook body.
func decodePayload(r *http.Request) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		payload := map[string]any{}
		for k, v := range vals {
			if len(v) == 1 {
				payload[k] = v[0]
			} else {
				payload[k] = v
			}
		}
		return payload, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("не удалось разобрать JSON: %w", err)
	}
	return payload, nil
}

// webhookReceive handles POST /hooks/{id} from payment systems.
func (s *Server) webhookReceive(w http.ResponseWriter, r *http.Request) {
	wh, err := s.Store.WebhookByID(r.Context(), r.PathValue("id"))
	if err == store.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	secret := r.Header.Get("X-Webhook-Secret")
	if secret == "" {
		secret = r.URL.Query().Get("secret")
	}
	if secret == "" || secret != wh.Secret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad secret"})
		return
	}
	payload, err := decodePayload(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	out := s.processWebhookPayload(r, wh, payload)
	status := http.StatusOK
	if !out.OK {
		// 200 anyway: payment systems should not retry known-bad payloads
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}

// ---- admin ----

func (s *Server) adminWebhooks(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListWebhooks(r.Context())
	if err != nil {
		s.renderError(w, r, 500, "Не удалось загрузить вебхуки")
		return
	}
	webinars, _ := s.Store.ListWebinars(r.Context())
	titles := map[string]string{}
	for _, wb := range webinars {
		titles[wb.ID] = wb.Title
	}
	s.render(w, http.StatusOK, "admin_webhooks.html", map[string]any{
		"Active":   "webhooks",
		"Title":    "Вебхуки",
		"Webhooks": list,
		"Titles":   titles,
		"AppURL":   s.Cfg.AppURL,
		"Flash":    s.flashes(r),
	})
}

func (s *Server) adminWebhookCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		flashRedirect(w, r, "/admin/webhooks", "invalid", true)
		return
	}
	wh := &model.Webhook{
		ID:       "wh_" + config.RandomHex(10),
		Name:     name,
		Secret:   "whsec_" + config.RandomHex(20),
		IsActive: true,
	}
	if err := s.Store.CreateWebhook(r.Context(), wh); err != nil {
		flashRedirect(w, r, "/admin/webhooks", "notfound", true)
		return
	}
	http.Redirect(w, r, "/admin/webhooks/"+wh.ID+"?ok=created", http.StatusSeeOther)
}

func (s *Server) loadWebhook(w http.ResponseWriter, r *http.Request) *model.Webhook {
	wh, err := s.Store.WebhookByID(r.Context(), r.PathValue("id"))
	if err == store.ErrNotFound {
		s.renderError(w, r, 404, "Вебхук не найден")
		return nil
	}
	if err != nil {
		s.renderError(w, r, 500, "Ошибка базы данных")
		return nil
	}
	return wh
}

// webhookView is the data for the webhook edit page.
type webhookView struct {
	*model.Webhook
	ProductsText string // "product_id = slug" lines
}

func (s *Server) adminWebhookEdit(w http.ResponseWriter, r *http.Request) {
	wh := s.loadWebhook(w, r)
	if wh == nil {
		return
	}
	webinars, _ := s.Store.ListWebinars(r.Context())
	slugByID := map[string]string{}
	for _, wb := range webinars {
		slugByID[wb.ID] = wb.Slug
	}
	prods, _ := s.Store.ListWebhookProducts(r.Context(), wh.ID)
	var lines []string
	for _, p := range prods {
		if slug := slugByID[p.WebinarID]; slug != "" {
			lines = append(lines, p.ExternalID+" = "+slug)
		}
	}
	view := webhookView{Webhook: wh}
	view.ProductsText = strings.Join(lines, "\n")

	s.render(w, http.StatusOK, "admin_webhook_edit.html", map[string]any{
		"Active":     "webhooks",
		"Title":      wh.Name,
		"Webhook":    view,
		"Webinars":   webinars,
		"Presets":    presets,
		"HookURL":    s.Cfg.AppURL + "/hooks/" + wh.ID,
		"HasDefault": wh.DefaultWebinarID != nil,
		"DefaultID":  derefString(wh.DefaultWebinarID),
		"Flash":      s.flashes(r),
		"TestResult": testResultFromQuery(r),
	})
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func testResultFromQuery(r *http.Request) string {
	v := r.URL.Query().Get("test")
	if v == "" {
		return ""
	}
	var out webhookOutcome
	if json.Unmarshal([]byte(v), &out) == nil {
		b, _ := json.MarshalIndent(out, "", "  ")
		return string(b)
	}
	return ""
}

// parseProductsText parses "external_id = webinar-slug" lines into mappings.
func (s *Server) parseProductsText(r *http.Request, text string) []model.WebhookProduct {
	var out []model.WebhookProduct
	slugToID := map[string]string{}
	webinars, _ := s.Store.ListWebinars(r.Context())
	for _, wb := range webinars {
		slugToID[wb.Slug] = wb.ID
		slugToID[wb.ID] = wb.ID // allow raw ids too
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		ext := strings.TrimSpace(parts[0])
		slug := strings.TrimSpace(parts[1])
		if id, ok := slugToID[slug]; ok && ext != "" {
			out = append(out, model.WebhookProduct{WebhookID: r.PathValue("id"), ExternalID: ext, WebinarID: id})
		}
	}
	return out
}

func (s *Server) adminWebhookUpdate(w http.ResponseWriter, r *http.Request) {
	wh := s.loadWebhook(w, r)
	if wh == nil {
		return
	}
	wh.Name = strings.TrimSpace(r.PostFormValue("name"))
	if wh.Name == "" {
		flashRedirect(w, r, "/admin/webhooks/"+wh.ID, "invalid", true)
		return
	}
	wh.EmailPath = strings.TrimSpace(r.PostFormValue("email_path"))
	wh.ProductPath = strings.TrimSpace(r.PostFormValue("product_path"))
	wh.IsActive = r.PostFormValue("is_active") == "on"
	if v := r.PostFormValue("default_webinar_id"); v != "" {
		wh.DefaultWebinarID = &v
	} else {
		wh.DefaultWebinarID = nil
	}
	if err := s.Store.UpdateWebhook(r.Context(), wh); err != nil {
		flashRedirect(w, r, "/admin/webhooks/"+wh.ID, "notfound", true)
		return
	}
	prods := s.parseProductsText(r, r.PostFormValue("products"))
	if err := s.Store.ReplaceWebhookProducts(r.Context(), wh.ID, prods); err != nil {
		flashRedirect(w, r, "/admin/webhooks/"+wh.ID, "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webhooks/"+wh.ID, "saved", false)
}

func (s *Server) adminWebhookDelete(w http.ResponseWriter, r *http.Request) {
	wh := s.loadWebhook(w, r)
	if wh == nil {
		return
	}
	if err := s.Store.DeleteWebhook(r.Context(), wh.ID); err != nil {
		flashRedirect(w, r, "/admin/webhooks", "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webhooks", "deleted", false)
}

func (s *Server) adminWebhookRegenSecret(w http.ResponseWriter, r *http.Request) {
	wh := s.loadWebhook(w, r)
	if wh == nil {
		return
	}
	if err := s.Store.WebhookRegenSecret(r.Context(), wh.ID, "whsec_"+config.RandomHex(20)); err != nil {
		flashRedirect(w, r, "/admin/webhooks/"+wh.ID, "notfound", true)
		return
	}
	flashRedirect(w, r, "/admin/webhooks/"+wh.ID, "secret-regen", false)
}

// adminWebhookTest runs the sample payload through the real pipeline.
func (s *Server) adminWebhookTest(w http.ResponseWriter, r *http.Request) {
	wh := s.loadWebhook(w, r)
	if wh == nil {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(r.PostFormValue("payload")), &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	out := s.processWebhookPayload(r, wh, payload)
	b, _ := json.Marshal(out)
	to := "/admin/webhooks/" + wh.ID + "?ok=saved&test=" + url.QueryEscape(string(b))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"encode"}`
	}
	return string(b)
}
