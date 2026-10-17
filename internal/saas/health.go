package saas

import (
	"context"
	"fmt"
	"github.com/zadstack/gorun/internal/admin"
)

// healthChecks reports actual imported configuration gaps without activating external integrations.
func (s *Service) healthChecks(ctx context.Context, v any) (any, error) {
	checks, ok := v.(*[]admin.HealthCheck)
	if !ok || checks == nil {
		return v, nil
	}
	queries := []struct{ name, query, detail string }{
		{"SaaS external protocol", `SELECT count(*) FROM saas_key_versions WHERE protocol='legacy-unconfigured' AND revoked_at='' AND status IN ('active','grace')`, "مفاتيح منقولة تحتاج عقد توقيع واشتقاق مفتاح مثبت قبل تفعيل العملاء الخارجيين؛ عرض الاشتراكات والاستهلاك المحلي يعملان."},
		{"SaaS subscription ownership", `SELECT count(*) FROM saas_entitlements WHERE owner_id IS NULL`, "اشتراكات بدون حساب مالك مثبت؛ اربط مستخدم المصدر بحساب GoRun قبل إتاحتها. لا تُعرض تلقائيًا لأي عميل."},
		{"SaaS external fulfillment", `SELECT count(*) FROM saas_apps WHERE status='active' AND mode='external' AND fulfillment_enabled=0`, "تطبيقات نشطة لكن التشغيل الخارجي غير مهيّأ. يلزم محول تشغيل موثق واختبار مستقل؛ لا يتم التنفيذ تلقائيًا."},
	}
	for _, q := range queries {
		var n int
		if e := s.DB.QueryRowContext(ctx, q.query).Scan(&n); e != nil {
			return checks, e
		}
		if n > 0 {
			*checks = append(*checks, admin.HealthCheck{Name: q.name, Status: admin.CheckWarn, Detail: fmt.Sprintf("%d: %s", n, q.detail)})
		}
	}
	return checks, nil
}
