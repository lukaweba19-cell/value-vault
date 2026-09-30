// Package payments parses the Microsoft payment instruments/transactions
// payloads into the "Active/Inactive/Country" hit fields.
package payments

import (
	"encoding/json"
	"fmt"
	"strings"

	"xbox-checker/internal/nettls"
)

// TranslateCountry maps a small set of country codes to readable names.
func TranslateCountry(code string) string {
	countries := map[string]string{
		"US": "United States", "GB": "United Kingdom", "CA": "Canada",
		"AU": "Australia", "DE": "Germany", "FR": "France", "BR": "Brazil",
	}
	if name, ok := countries[strings.ToUpper(code)]; ok {
		return name
	}
	return code
}

// Parse extracts cash balances, active non-cash methods, removed methods and
// the account country from the paymentInstrumentsEx JSON payload.
func Parse(payStr string) (cashStrs []string, nonCashActive []string, removedMethods []string, country string) {
	countryCode := nettls.ParseBetween(payStr, "country\":\"", "\"")
	if countryCode == "" {
		countryCode = "US"
	}
	country = TranslateCountry(countryCode)

	var rawJSON interface{}
	if err := json.Unmarshal([]byte(payStr), &rawJSON); err == nil {
		var list []interface{}
		switch v := rawJSON.(type) {
		case []interface{}:
			list = v
		case map[string]interface{}:
			for _, key := range []string{"instrumentList", "paymentInstruments", "instruments", "value"} {
				if arr, ok := v[key].([]interface{}); ok {
					list = arr
					break
				}
			}
			if len(list) == 0 {
				list = []interface{}{v}
			}
		}

		asString := func(v interface{}) string {
			switch t := v.(type) {
			case string:
				return strings.TrimSpace(t)
			case float64:
				if t == float64(int64(t)) {
					return fmt.Sprintf("%d", int64(t))
				}
				return strings.TrimSpace(fmt.Sprintf("%v", t))
			case json.Number:
				return t.String()
			case int:
				return fmt.Sprintf("%d", t)
			default:
				return ""
			}
		}

		brandFromType := func(pmType string) string {
			switch strings.ToLower(strings.TrimSpace(pmType)) {
			case "visa":
				return "Visa"
			case "mc", "mastercard", "master_card":
				return "MasterCard"
			case "amex", "americanexpress", "american_express":
				return "Amex"
			case "discover":
				return "Discover"
			case "jcb":
				return "JCB"
			case "diners", "dinersclub", "diners_club":
				return "Diners"
			case "unionpay", "union_pay":
				return "UnionPay"
			case "maestro":
				return "Maestro"
			default:
				return ""
			}
		}

		for _, item := range list {
			if obj, ok := item.(map[string]interface{}); ok {
				instrType, _ := obj["instrumentType"].(string)
				status, _ := obj["status"].(string)

				var brand, last4, expMonth, expYear, ppEmail, pmType, displayName string

				if pm, ok := obj["paymentMethod"].(map[string]interface{}); ok {
					pmType, _ = pm["paymentMethodType"].(string)
					if instrType == "" {
						instrType = pmType
					}
					if disp, ok := pm["display"].(map[string]interface{}); ok {
						displayName, _ = disp["name"].(string)
					}
				}

				if details, ok := obj["details"].(map[string]interface{}); ok {
					brand = asString(details["brand"])
					last4 = asString(details["lastFourDigits"])
					if last4 == "" {
						last4 = asString(details["last4"])
					}
					expMonth = asString(details["expiryMonth"])
					expYear = asString(details["expiryYear"])
					ppEmail = asString(details["email"])
				}

				if brand == "" {
					brand = asString(obj["brand"])
				}
				if brand == "" {
					brand = strings.TrimSpace(displayName)
				}
				if brand == "" {
					brand = brandFromType(pmType)
				}
				if last4 == "" {
					last4 = asString(obj["lastFourDigits"])
				}
				if expMonth == "" {
					expMonth = asString(obj["expiryMonth"])
				}
				if expYear == "" {
					expYear = asString(obj["expiryYear"])
				}
				if ppEmail == "" {
					ppEmail = asString(obj["email"])
				}

				isRemoved := strings.EqualFold(status, "removed")
				statusLabel := "Active"
				if isRemoved {
					statusLabel = "Removed"
				}

				var detail string
				lowerType := strings.ToLower(instrType)
				lowerBrand := strings.ToLower(brand)

				if strings.Contains(lowerType, "stored_value") || strings.Contains(lowerType, "gift_card") {
					if details, ok := obj["details"].(map[string]interface{}); ok {
						curr, _ := details["currency"].(string)
						if bal, ok := details["balance"].(float64); ok {
							if bal > 0 {
								detail = fmt.Sprintf("cash: %.2f %s", bal, curr)
							}
						}
					}
				} else if strings.Contains(lowerType, "paypal") || strings.Contains(lowerBrand, "paypal") || (ppEmail != "" && last4 == "") {
					if ppEmail != "" && !strings.Contains(ppEmail, "-") && len(ppEmail) <= 40 && strings.Contains(ppEmail, "@") {
						detail = fmt.Sprintf("PayPal (%s): %s", statusLabel, ppEmail)
					}
				} else if last4 != "" || (brand != "" && brandFromType(pmType) != "") {
					if brand == "" {
						brand = "Card"
					}
					if expMonth == "" {
						expMonth = "N/A"
					}
					if expYear == "" {
						expYear = "N/A"
					}
					// "Visa card 1234" — if the brand already ends with "card", don't double it
					if strings.HasSuffix(strings.ToLower(brand), "card") {
						detail = fmt.Sprintf("%s %s [%s] | Expires: %s/%s", brand, last4, statusLabel, expMonth, expYear)
					} else {
						detail = fmt.Sprintf("%s card %s [%s] | Expires: %s/%s", brand, last4, statusLabel, expMonth, expYear)
					}
				}

				if detail != "" {
					if isRemoved {
						removedMethods = append(removedMethods, detail)
					} else {
						if strings.HasPrefix(detail, "cash:") {
							cashStrs = append(cashStrs, detail)
						} else {
							nonCashActive = append(nonCashActive, detail)
						}
					}
				}
			}
		}
	}
	return
}
