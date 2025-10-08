package usecase

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type FunnelRepository interface {
	Hit(state State, chatID int64) error
	Counts() map[State]int
	// DailyActiveCounts возвращает количество уникальных chat_id по дням (YYYY-MM-DD) за последние days дней
	DailyActiveCounts(days int) map[string]int
}

type FunnelUsecase struct {
	repo  FunnelRepository
	order []State
}

func NewFunnelUsecase(repo FunnelRepository) *FunnelUsecase {
	return &FunnelUsecase{
		repo: repo,
		order: []State{
			StateIntro,
			StatePurpose,
			StateBedrooms,
			StatePayment,
			StateRequestPhone,
			StateLeadSaved,
		},
	}
}

func (u *FunnelUsecase) Reach(chatID int64, state State) {
	if state == "" {
		return
	}
	_ = u.repo.Hit(state, chatID)
}

func (u *FunnelUsecase) Chart() string {
	counts := u.repo.Counts()
	if len(counts) == 0 {
		return "Данных по воронке пока нет"
	}
	// base is first step count
	var base int
	if len(u.order) > 0 {
		base = counts[u.order[0]]
	}
	if base == 0 {
		// найти максимальный как базу
		for _, s := range u.order {
			if counts[s] > base {
				base = counts[s]
			}
		}
	}
	var prev int
	var b strings.Builder
	b.WriteString("Воронка по шагам:\n")
	for i, s := range u.order {
		c := counts[s]
		relBase := percent(c, base)
		relPrev := 0
		if i == 0 {
			relPrev = 100
		} else if prev > 0 {
			relPrev = percent(c, prev)
		}
		bar := bar20(c, base)
		fmt.Fprintf(&b, "- %s: %d | %3d%% от базового | %3d%% от пред. %s\n", stateLabel(s), c, relBase, relPrev, bar)
		prev = c
	}
	return b.String()
}

// GraphData возвращает метки и значения по порядку шагов для построения графика
func (u *FunnelUsecase) GraphData() ([]string, []int) {
	counts := u.repo.Counts()
	labels := make([]string, 0, len(u.order))
	values := make([]int, 0, len(u.order))
	for _, s := range u.order {
		labels = append(labels, stateLabel(s))
		values = append(values, counts[s])
	}
	return labels, values
}

// DailyActive возвращает метки дней и значения DAU за последние days дней (включая сегодня)
func (u *FunnelUsecase) DailyActive(days int) ([]string, []int) {
	if days <= 0 {
		days = 7
	}
	counts := u.repo.DailyActiveCounts(days)
	// логируем сырые значения DAU для отладки
	if len(counts) == 0 {
		slog.Default().Info("dau counts empty", "days", days)
	} else {
		slog.Default().Info("dau counts", "days", days, "counts", counts)
	}
	labels := make([]string, 0, days)
	values := make([]int, 0, days)
	// построим последовательность дат, чтобы заполнить нулями пропуски
	for i := days - 1; i >= 0; i-- {
		d := dateMinus(i)
		labels = append(labels, d)
		values = append(values, counts[d])
	}
	return labels, values
}

// dateMinus возвращает YYYY-MM-DD для сегодняшней даты минус deltaDays
func dateMinus(deltaDays int) string {
	d := time.Now().AddDate(0, 0, -deltaDays)
	return d.Format("2006-01-02")
}

func percent(a, b int) int {
	if b <= 0 {
		return 0
	}
	return int((100 * a) / b)
}

func bar20(val, max int) string {
	if max <= 0 {
		return ""
	}
	filled := (20 * val) / max
	if filled < 0 {
		filled = 0
	}
	if filled > 20 {
		filled = 20
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", 20-filled) + "]"
}

func stateLabel(s State) string {
	switch s {
	case StateIntro:
		return "Приветствие"
	case StatePurpose:
		return "Цель"
	case StateBedrooms:
		return "Спальни"
	case StatePayment:
		return "Оплата"
	case StateRequestPhone:
		return "Запрос номера"
	case StateLeadSaved:
		return "Лид"
	case StateFinalMessage:
		return "Оффер и каталог"
	default:
		return string(s)
	}
}
