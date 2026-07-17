package agent

import (
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

const noMarkdownRule = `Javobni oddiy matn sifatida yoz — Telegram xabari
markdown formatlashni (**qalin**, __tagiz__, # sarlavha, - ro'yxat belgisi va
h.k.) ko'rsatmaydi, shuning uchun bunday belgilardan mutlaqo foydalanma.
Kerak bo'lsa emoji yoki oddiy tire (-) bilan ro'yxat qilishing mumkin.`

const baseSystemPrompt = `Sen — Billz POS ma'lumotlari va vazifalar bo'yicha
yordam beruvchi shaxsiy AI-agentsan. Foydalanuvchi — biznes egasi.

MUHIM QOIDALAR:
- Sen faqat MA'LUMOTNI O'QIYSAN — Billz'dagi hech narsani o'zgartira olmaysan
  va o'zgartirmaysan (bunday vosita senda umuman yo'q). Har doim faqat
  hisobot/tahlil ber.
- Agar so'rovda qaysi do'kon yoki qaysi davr aniq bo'lmasa — taxmin qilma,
  qisqa aniqlashtiruvchi savol ber (masalan "qaysi do'kon?" yoki "qaysi
  davr — shu oymi, o'tgan oymi?"). Faqat aniq bo'lgandan keyin vositalarni
  chaqir.
- Javobni odatda oddiy matn qil. Excel fayl faqat foydalanuvchi aniq so'rasa
  ("Excel qilib ber", "fayl yubor" va h.k.) yoki natija juda ko'p qatordan
  iborat bo'lsa (15+ qator) generate_excel_report vositasini chaqir.
- Sanalarni YYYY-MM-DD formatida ber. Aksincha ko'rsatilmasa, davr
  so'ralganida joriy oy boshidan bugungi kungacha deb hisobla.
- Qisqa va aniq gapir, ortiqcha so'zlarni tashla.

` + noMarkdownRule

func systemPrompt(now time.Time) string {
	return fmt.Sprintf("%s\n\nHozirgi sana va vaqt: %s (Asia/Tashkent).",
		baseSystemPrompt, now.Format("2006-01-02 15:04, Monday"))
}

func tool(name, description string, properties map[string]any, required []string) anthropic.ToolUnionParam {
	return anthropic.ToolUnionParam{
		OfTool: &anthropic.ToolParam{
			Name:        name,
			Description: param.NewOpt(description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: properties,
				Required:   required,
			},
		},
	}
}

var (
	dateProps = map[string]any{
		"start_date": map[string]any{"type": "string", "description": "Davr boshi, YYYY-MM-DD"},
		"end_date":   map[string]any{"type": "string", "description": "Davr oxiri, YYYY-MM-DD"},
		"shop_id":    map[string]any{"type": "string", "description": "Aniq do'kon ID (ixtiyoriy — bo'sh bo'lsa barcha do'konlar bo'yicha)"},
	}

	allTools = []anthropic.ToolUnionParam{
		tool("get_revenue_report",
			"Berilgan davr uchun savdo/foyda hisobotini beradi — kompaniya bo'yicha yoki bitta do'kon uchun.",
			dateProps, []string{"start_date", "end_date"}),

		tool("compare_periods",
			"Ikkita ixtiyoriy davrni solishtiradi (masalan bu oy va o'tgan oy, yoki bu hafta va o'tgan hafta) — savdo, tranzaksiya, foyda bo'yicha farq foizi bilan.",
			map[string]any{
				"period_a_start": map[string]any{"type": "string", "description": "Birinchi davr boshi, YYYY-MM-DD"},
				"period_a_end":   map[string]any{"type": "string", "description": "Birinchi davr oxiri, YYYY-MM-DD"},
				"period_b_start": map[string]any{"type": "string", "description": "Ikkinchi davr boshi, YYYY-MM-DD"},
				"period_b_end":   map[string]any{"type": "string", "description": "Ikkinchi davr oxiri, YYYY-MM-DD"},
				"shop_id":        map[string]any{"type": "string", "description": "Aniq do'kon ID (ixtiyoriy)"},
			}, []string{"period_a_start", "period_a_end", "period_b_start", "period_b_end"}),

		tool("seller_performance",
			"Berilgan davrda eng ko'p sotgan sotuvchilar reytingini beradi.",
			map[string]any{
				"start_date": dateProps["start_date"],
				"end_date":   dateProps["end_date"],
				"shop_id":    dateProps["shop_id"],
				"top_n":      map[string]any{"type": "integer", "description": "Nechta sotuvchi ko'rsatish kerak (default 10)"},
			}, []string{"start_date", "end_date"}),

		tool("seller_year_over_year",
			"Bitta sotuvchining savdosini shu davrning bir yil oldingi bilan solishtiradi — 'X sotuvchini o'tgan yil bilan solishtir' kabi so'rovlar uchun asosiy vosita.",
			map[string]any{
				"seller_name":  map[string]any{"type": "string", "description": "Sotuvchining ismi (to'liq yoki qisman)"},
				"shop_id":      map[string]any{"type": "string", "description": "Aniq do'kon ID (ixtiyoriy — bo'sh bo'lsa barcha do'konlar)"},
				"period_start": map[string]any{"type": "string", "description": "Davr boshi YYYY-MM-DD (ixtiyoriy, bo'lmasa joriy oy boshidan)"},
				"period_end":   map[string]any{"type": "string", "description": "Davr oxiri YYYY-MM-DD (ixtiyoriy, bo'lmasa bugun)"},
			}, []string{"seller_name"}),

		tool("product_sales_report",
			"Berilgan davrda eng ko'p (yoki eng kam) sotilgan tovarlar ro'yxatini beradi.",
			map[string]any{
				"start_date": dateProps["start_date"],
				"end_date":   dateProps["end_date"],
				"shop_id":    dateProps["shop_id"],
				"top_n":      map[string]any{"type": "integer", "description": "Nechta tovar ko'rsatish kerak (default 10)"},
				"order":      map[string]any{"type": "string", "enum": []string{"top", "bottom"}, "description": "top = eng ko'p sotilgan, bottom = eng kam sotilgan"},
			}, []string{"start_date", "end_date"}),

		tool("category_breakdown",
			"Berilgan davrda savdoni tovar kategoriyalari bo'yicha taqsimlab beradi.",
			dateProps, []string{"start_date", "end_date"}),

		tool("payment_breakdown",
			"Berilgan davrda savdoni to'lov usullari (naqd, Click, Payme va h.k.) bo'yicha taqsimlab beradi.",
			dateProps, []string{"start_date", "end_date"}),

		tool("stock_levels",
			"Ombordagi joriy qoldiqni ko'rsatadi — bitta do'kon uchun yoki tovar nomi/SKU bo'yicha qidiruv orqali kompaniya bo'yicha.",
			map[string]any{
				"shop_id":       map[string]any{"type": "string", "description": "Aniq do'kon ID (ixtiyoriy — bo'lsa faqat shu do'kon qoldig'i tekshiriladi)"},
				"product_query": map[string]any{"type": "string", "description": "Tovar nomi yoki SKU bo'yicha qidiruv (ixtiyoriy)"},
			}, nil),

		tool("sales_forecast",
			"Joriy oy oxirigacha bo'lgan savdo prognozini beradi (haftalik trend va o'tgan yil bilan solishtirilgan holda). Parametrsiz — doim joriy oy uchun.",
			map[string]any{}, nil),

		tool("add_task",
			"Yangi vazifa qo'shadi.",
			map[string]any{
				"description": map[string]any{"type": "string", "description": "Vazifa matni"},
				"due_at":      map[string]any{"type": "string", "description": "Muddat, ISO 8601 (masalan 2026-07-15T15:00:00+05:00). Aytilmagan bo'lsa bo'sh qoldiring."},
			}, []string{"description"}),

		tool("list_tasks", "Ochiq vazifalar ro'yxatini beradi.", map[string]any{}, nil),

		tool("complete_task",
			"Vazifani bajarilgan deb belgilaydi (vazifa saqlanadi, faqat 'bajarildi' deb belgilanadi).",
			map[string]any{"task_id": map[string]any{"type": "integer", "description": "Vazifa raqami"}},
			[]string{"task_id"}),

		tool("delete_task",
			"Vazifani butunlay o'chiradi (masalan xato qo'shilgan bo'lsa) — complete_task'dan farqli o'laroq, vazifa bazadan butunlay o'chadi va qaytarib bo'lmaydi.",
			map[string]any{"task_id": map[string]any{"type": "integer", "description": "Vazifa raqami"}},
			[]string{"task_id"}),

		tool("get_daily_summary", "Bugungi kunlik xulosani (vazifalar, tadbirlar) beradi.", map[string]any{}, nil),

		tool("generate_excel_report",
			"Jadval ma'lumotlarini Excel (.xlsx) faylga aylantiradi va foydalanuvchiga alohida fayl sifatida yuboradi. Faqat aniq so'ralganda yoki qatorlar soni ko'p bo'lganda chaqiring.",
			map[string]any{
				"title":   map[string]any{"type": "string", "description": "Hisobot sarlavhasi (fayl varag'i nomi bo'ladi)"},
				"columns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Ustun sarlavhalari"},
				"rows":    map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "description": "Har bir qator — satrlar massivi (ustunlar bilan mos tartibda)"},
			}, []string{"title", "columns", "rows"}),
	}
)
