package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/latch/backend/internal/httpx"
)

// supportedCurrencies are the fiat display currencies GET /v1/prices accepts,
// each an ISO 4217 code CoinGecko supports as a vs_currency. Anything else is
// rejected with a 400 naming this list (see the Swagger annotation). Keep
// entries lowercase — the handler normalizes input before checking.
var supportedCurrencies = map[string]struct{}{
	"aed": {}, "ars": {}, "aud": {}, "brl": {}, "cad": {}, "chf": {}, "clp": {},
	"cny": {}, "cop": {}, "czk": {}, "dkk": {}, "eur": {}, "gbp": {}, "hkd": {},
	"idr": {}, "ils": {}, "inr": {}, "jpy": {}, "krw": {}, "mxn": {}, "ngn": {},
	"nok": {}, "nzd": {}, "php": {}, "pln": {}, "rub": {}, "sar": {}, "sek": {},
	"sgd": {}, "thb": {}, "try": {}, "uah": {}, "usd": {}, "vnd": {}, "zar": {},
}

// supportedCurrencyList is the sorted form of supportedCurrencies, for error
// messages that name every supported code.
var supportedCurrencyList = func() []string {
	list := make([]string, 0, len(supportedCurrencies))
	for c := range supportedCurrencies {
		list = append(list, c)
	}
	sort.Strings(list)
	return list
}()

const defaultCurrency = "usd"

type PricesHandler struct {
	priceSvc priceService
}

func NewPricesHandler(priceSvc priceService) *PricesHandler {
	return &PricesHandler{priceSvc: priceSvc}
}

// GetPrices godoc
// @Summary      Live prices
// @Description  Returns the current price for each requested Stellar asset, quoted in the requested fiat currency (default usd). Results are Redis-cached for 60 seconds, per currency. The response names the currency it is quoted in via the top-level `currency` field; `data` maps each requested token to {"price", "change_24h"}. Supported currencies (ISO 4217, lowercase): aed, ars, aud, brl, cad, chf, clp, cny, cop, czk, dkk, eur, gbp, hkd, idr, ils, inr, jpy, krw, mxn, ngn, nok, nzd, php, pln, rub, sar, sek, sgd, thb, try, uah, usd, vnd, zar. An unsupported currency returns 400.
// @Tags         market
// @Produce      json
// @Param        tokens   query string false "Comma-separated token symbols" example(native,xlm)
// @Param        currency query string false "ISO 4217 currency code (lowercase); defaults to usd" default(usd) example(usd)
// @Success      200 {object} pricesDataResponse
// @Failure      400 {object} apiErrorResponse
// @Router       /v1/prices [get]
func (h *PricesHandler) GetPrices(c *gin.Context) {
	raw := c.Query("tokens")
	if raw == "" {
		raw = "native"
	}

	var tokens []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation, "tokens param is required")
		return
	}

	currency := strings.ToLower(strings.TrimSpace(c.Query("currency")))
	if currency == "" {
		currency = defaultCurrency
	}
	if _, ok := supportedCurrencies[currency]; !ok {
		httpx.Fail(c, http.StatusBadRequest, httpx.ErrValidation,
			fmt.Sprintf("unsupported currency %q; supported: %s", c.Query("currency"), strings.Join(supportedCurrencyList, ", ")))
		return
	}

	prices := h.priceSvc.GetPrices(c.Request.Context(), tokens, currency)
	httpx.SuccessWithMeta(c, http.StatusOK, prices, "currency", currency)
}
