package usecase

// Логические состояния и ответы, независимые от Telegram

type State string

const (
	StateStart        State = "start"
	StateIntro              = "intro"
	StatePurpose            = "purpose"
	StateBedrooms           = "bedrooms"
	StatePayment            = "payment"
	StateFinalMessage       = "final_message"
	StateRequestPhone       = "request_phone"
	StateLeadSaved          = "lead_saved"
)

const (
	PurposeSelf     = "Для жизни"
	PurposeRelative = "Для близких"
	PurposeInvest   = "Для инвестиций"

	Bedrooms1     = "1 спальня"
	Bedrooms2     = "2 спальни"
	Bedrooms3Plus = "3 и более спален"

	PaymentCash        = "100% собственных средств"
	PaymentInstallment = "Бесплатная рассрочка"
	PaymentMortgage    = "Ипотека"
	PaymentTradeIn     = "Трейд-ин"

	StartBtn = "Хочу"

	ChannelWhatsApp   = "В Telegram"
	ChannelExpertCall = "Связаться с экспертом"
)

type Session struct {
	State    State
	Purpose  string
	Bedrooms string
	Payment  string
	Phone    string
}

type Reply struct {
	Text           string
	Options        []string
	RemoveKeyboard bool
	AdvanceTo      State
}

type Dialog struct{}

func NewDialog() *Dialog { return &Dialog{} }

func (d *Dialog) Handle(s *Session, text string) Reply {
	if text == "/start" || s.State == StateStart {
		s.State = StateIntro
		greet := "Вас приветствует команда концептуального жилого комплекса «ЗИМ Галерея» - новаторского проекта бизнес-класса в Самаре с уникальным домом галерейного типа и квартирами-ячейками, вдохновлёнными легендарным Домом Наркомфина."
		return Reply{Text: greet, Options: []string{StartBtn}, AdvanceTo: StateIntro}
	}

	switch s.State {
	case StateIntro:
		if text == StartBtn {
			// Сразу задаем первый вопрос
			s.State = StatePurpose
			return Reply{Text: "Для каких целей рассматриваете квартиру?", Options: []string{PurposeSelf, PurposeRelative, PurposeInvest}, AdvanceTo: StatePurpose}
		}
		return Reply{Text: "Нажмите 'Хочу'", Options: []string{StartBtn}}

	case StatePurpose:
		if text == PurposeSelf || text == PurposeRelative || text == PurposeInvest {
			s.Purpose = text
			s.State = StateBedrooms
			return Reply{Text: "Сколько спален необходимо?", Options: []string{Bedrooms1, Bedrooms2, Bedrooms3Plus}, AdvanceTo: StateBedrooms}
		}
		return Reply{Text: "Пожалуйста, выберите вариант", Options: []string{PurposeSelf, PurposeRelative, PurposeInvest}}

	case StateBedrooms:
		if text == Bedrooms1 || text == Bedrooms2 || text == Bedrooms3Plus {
			s.Bedrooms = text
			s.State = StatePayment
			return Reply{Text: "Какая форма оплаты предпочтительна?", Options: []string{PaymentCash, PaymentInstallment, PaymentMortgage, PaymentTradeIn}, AdvanceTo: StatePayment}
		}
		return Reply{Text: "Пожалуйста, выберите количество спален", Options: []string{Bedrooms1, Bedrooms2, Bedrooms3Plus}}

	case StatePayment:
		if text == PaymentCash || text == PaymentInstallment || text == PaymentMortgage || text == PaymentTradeIn {
			s.Payment = text
			// Сразу отдаём ценность и просим номер
			s.State = StateRequestPhone
			msg := finalTextForSelection(s) + "\n\n" + "Оставьте номер — вышлю точный расчет и 2–3 альтернативы."
			return Reply{Text: msg, AdvanceTo: StateRequestPhone}
		}
		return Reply{Text: "Пожалуйста, выберите способ оплаты", Options: []string{PaymentCash, PaymentInstallment, PaymentMortgage, PaymentTradeIn}}

		// Шаг выбора канала удалён
	}

	return Reply{Text: "Не понял команду"}
}

func finalTextForSelection(s *Session) string {
	// Сообщение по финансовой программе
	var offer string
	switch s.Payment {
	case PaymentCash:
		offer = "Те, кто использует 100% собственных средств при оплате, могут получить специальную премию от 3% до 7% от цены квартиры. Хотите получить подробный расчет?"
	case PaymentInstallment:
		offer = "Бесплатная рассрочка от застройщика гибко подстраивается под ваши запросы, можно выбрать размер первого взноса от 20% и удобную схему платежей – каждый месяц/квартал/полгода. Хотите получить подробный расчет?"
	default:
		// Фолбэк на нейтральную формулировку
		offer = "Подготовим персональную подборку и расчёты с учётом ваших параметров. Хотите получить подробный расчет?"
	}

	return offer
}

func CatalogTitleFor(s *Session) string {
	if s.Purpose == PurposeInvest {
		return "Топ-10 эксклюзивных квартир"
	}
	switch s.Purpose {
	case PurposeSelf:
		switch s.Bedrooms {
		case Bedrooms1:
			return "Топ квартир с одной спальней для жизни"
		case Bedrooms2:
			return "Топ квартир с двумя спальнями для жизни"
		case Bedrooms3Plus:
			return "Топ квартир с тремя и более спальнями для жизни"
		}
	case PurposeRelative:
		switch s.Bedrooms {
		case Bedrooms1:
			return "Топ квартир с одной спальней для близких"
		case Bedrooms2:
			return "Топ квартир с двумя спальнями для близких"
		case Bedrooms3Plus:
			return "Топ квартир с тремя и более спальнями для близких"
		}
	}
	return ""
}

// CatalogFileFor возвращает путь к PDF в папке collections согласно выбору пользователя.
func CatalogFileFor(s *Session) string {
	if s.Purpose == PurposeInvest {
		return "collections/топ-10_эксклюзивных_квартир.pdf"
	}
	switch s.Purpose {
	case PurposeSelf:
		switch s.Bedrooms {
		case Bedrooms1:
			return "collections/топ_квартир_с_одной_спальней_для_жизни.pdf"
		case Bedrooms2:
			return "collections/топ_квартир_с_двумя_спальнями_для_жизни.pdf"
		case Bedrooms3Plus:
			return "collections/топ_квартир_с_тремя_и_более_спальнями_для_жизни.pdf"
		}
	case PurposeRelative:
		switch s.Bedrooms {
		case Bedrooms1:
			return "collections/топ_квартир_с_одной_спальней_для_близких.pdf"
		case Bedrooms2:
			return "collections/топ_квартир_с_двумя_спальнями_для_близких.pdf"
		case Bedrooms3Plus:
			return "collections/топ_квартир_с_тремя_и_более_спальнями_для_близких.pdf"
		}
	}
	return ""
}
