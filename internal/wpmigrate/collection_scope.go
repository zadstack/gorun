package wpmigrate

import "fmt"

func validateRESTContentState(item WPContent, authed bool) error {
	if item.Status == "publish" {
		return nil
	}
	if authed {
		switch item.Status {
		case "draft", "pending", "future", "private":
			return nil
		}
	}
	return fmt.Errorf("%w: REST content state outside requested scope (type %s, source ID %d)", ErrUnavailable, item.Type, item.ID)
}

func collectionNote(r CollectionRead, authed bool) map[string]string {
	en, ar := "Published content only; anonymous access.", "المحتوى المنشور فقط؛ قراءة بدون مصادقة."
	if authed {
		en = "Requested publish, draft, pending, future and private; only content readable by this account. Trash, auto-draft and custom states are excluded."
		ar = "طُلب المنشور والمسودة وقيد المراجعة والمجدول والخاص؛ يُنقل فقط ما يستطيع هذا الحساب قراءته. المحذوف والمسودات التلقائية والحالات المخصصة مستبعدة."
	}
	if !r.CountKnown || !r.PagesKnown || r.Visible != r.ReportedTotal {
		totalEN, totalAR, pagesEN, pagesAR := "unknown", "غير معروف", "unknown", "غير معروف"
		if r.CountKnown {
			totalEN = fmt.Sprint(r.ReportedTotal)
			totalAR = totalEN
		}
		if r.PagesKnown {
			pagesEN = fmt.Sprint(r.ReportedPages)
			pagesAR = pagesEN
		}
		en += fmt.Sprintf(" Readable: %d. Source total is a permission-filtered bound, not proof of inaccessible content coverage (reported %s, pages %s).", r.Visible, totalEN, pagesEN)
		ar += fmt.Sprintf(" القابل للقراءة: %d. إجمالي المصدر حد قبل تصفية الصلاحيات ولا يثبت تغطية المحتوى المحجوب (المعلن %s، الصفحات %s).", r.Visible, totalAR, pagesAR)
	}
	return map[string]string{"en": en, "ar": ar}
}
