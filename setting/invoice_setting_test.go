package setting

import "testing"

func TestInvoiceConfigDefaults(t *testing.T) {
	cfg := InvoiceConfigFrom(func(string) string { return "" })
	if cfg.Enabled || cfg.HasKeys() {
		t.Fatalf("缺省应关闭且无密钥: %+v", cfg)
	}
	if cfg.ServiceUrl != "https://fapiao-api.easyapi.com" || cfg.TaxRate != "0.01" || cfg.ItemNo != "3040203" ||
		cfg.ItemName != "技术服务费" || cfg.Category != "全电电子普通发票" {
		t.Fatalf("缺省值不对: %+v", cfg)
	}

	db := map[string]string{
		InvoiceEnabled:    "1",
		InvoiceServiceUrl: " https://fp.example.com/ ",
		InvoiceAppKey:     "k",
		InvoiceAppSecret:  "s",
		InvoiceCategory:   "增值税专用发票",
	}
	cfg = InvoiceConfigFrom(func(k string) string { return db[k] })
	if !cfg.Enabled || !cfg.HasKeys() || cfg.ServiceUrl != "https://fp.example.com" || cfg.Category != "增值税专用发票" || cfg.TaxRate != "0.01" {
		t.Fatalf("DB 覆盖不对: %+v", cfg)
	}
}

// TestInvoiceSecretMasked 发票 appKey/appSecret 在 GET /admin/setting 里只下发占位值，其余发票项照常回显。
func TestInvoiceSecretMasked(t *testing.T) {
	m := map[string]string{
		InvoiceAppKey:     "real-key",
		InvoiceAppSecret:  "real-secret",
		InvoiceEnabled:    "true",
		InvoiceServiceUrl: DefaultInvoiceServiceUrl,
		InvoiceTaxRate:    "0.01",
		InvoiceItemNo:     "3040203",
		InvoiceItemName:   "技术服务费",
		InvoiceCategory:   "全电电子普通发票",
	}
	MaskSecretSettings(m)
	if m[InvoiceAppKey] != MaskedSecret || m[InvoiceAppSecret] != MaskedSecret {
		t.Fatalf("发票密钥应打码: %+v", m)
	}
	for _, k := range []string{InvoiceEnabled, InvoiceServiceUrl, InvoiceTaxRate, InvoiceItemNo, InvoiceItemName, InvoiceCategory} {
		if IsSecretSettingKey(k) || m[k] == MaskedSecret {
			t.Errorf("%s 不是密钥，不应打码", k)
		}
	}
}
