package workagents

import (
	"context"
	"fmt"

	"github.com/zadstack/gorun/internal/admin"
)

func (s *service) healthChecks(ctx context.Context, value any) (any, error) {
	checks, ok := value.(*[]admin.HealthCheck)
	if !ok || checks == nil {
		return value, nil
	}
	var unsupported, failed, enabled, pausedImported int
	if e := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_agents WHERE json_array_length(unsupported_tools)>0`).Scan(&unsupported); e != nil {
		return value, e
	}
	if e := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_runs WHERE historical=0 AND status='failed'`).Scan(&failed); e != nil {
		return value, e
	}
	if e := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_agents WHERE enabled=1 AND execution_kind='text'`).Scan(&enabled); e != nil {
		return value, e
	}
	if e := s.d.DB.QueryRowContext(ctx, `SELECT count(*) FROM workagents_workflows WHERE source_id<>'' AND enabled=0`).Scan(&pausedImported); e != nil {
		return value, e
	}
	status := admin.CheckOK
	detail := "لا توجد أدوات منقولة غير مدعومة"
	if unsupported > 0 {
		status = admin.CheckWarn
		detail = fmt.Sprintf("%d إيجنتس تحتاج تكييف أدوات؛ لا تُنفذ هذه الأدوات تلقائيًا", unsupported)
	}
	*checks = append(*checks, admin.HealthCheck{Name: "أدوات الإيجنتس المنقولة", Status: status, Detail: detail})
	if enabled > 0 && (s.d.Settings == nil || !admin.WorkAgentProviderReady(ctx, s.d, "", "")) {
		*checks = append(*checks, admin.HealthCheck{Name: "مزود AI للإيجنتس", Status: admin.CheckWarn, Detail: "المزود الافتراضي غير مهيأ؛ الإيجنتس ذات الاتصال الخاص تُراجع من إعداداتها"})
	}
	if failed > 0 {
		*checks = append(*checks, admin.HealthCheck{Name: "تشغيل الإيجنتس", Status: admin.CheckWarn, Detail: fmt.Sprintf("%d تشغيلات فعلية فشلت؛ راجع سجل التشغيل قبل اعتبار الموقع جاهزًا", failed)})
	}
	if pausedImported > 0 {
		*checks = append(*checks, admin.HealthCheck{Name: "الفلوهات المنقولة", Status: admin.CheckWarn, Detail: fmt.Sprintf("%d فلوهات منقولة متوقفة؛ راجع الطلب والجدولة قبل التفعيل. لا يُعاد تشغيل التاريخ القديم", pausedImported)})
	}
	return checks, nil
}
