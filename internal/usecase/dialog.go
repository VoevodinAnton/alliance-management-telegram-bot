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
	PurposeSelf     = "Для себя"
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
		greet := "Здравствуйте 👋\n\nСпасибо, что обратились к нам. Альянс‑Менеджмент — застройщик современных жилых комплексов. Поможем быстро подобрать квартиру в наших проектах и подготовим индивидуальные расчёты.\n\nХотите, чтобы мы подобрали для вас лучшие варианты или отправили расчёты по конкретному предложению?"
		return Reply{Text: greet, Options: []string{StartBtn}, AdvanceTo: StateIntro}
	}

	switch s.State {
	case StateIntro:
		if text == StartBtn {
			// Сразу задаем первый вопрос
			s.State = StatePurpose
			return Reply{Text: "Для кого вы подбираете квартиру?", Options: []string{PurposeSelf, PurposeRelative, PurposeInvest}, AdvanceTo: StatePurpose}
		}
		return Reply{Text: "Нажмите 'Хочу'", Options: []string{StartBtn}}

	case StatePurpose:
		if text == PurposeSelf || text == PurposeRelative || text == PurposeInvest {
			s.Purpose = text
			s.State = StateBedrooms
			return Reply{Text: "Сколько спален вы рассматриваете?", Options: []string{Bedrooms1, Bedrooms2, Bedrooms3Plus}, AdvanceTo: StateBedrooms}
		}
		return Reply{Text: "Пожалуйста, выберите вариант", Options: []string{PurposeSelf, PurposeRelative, PurposeInvest}}

	case StateBedrooms:
		if text == Bedrooms1 || text == Bedrooms2 || text == Bedrooms3Plus {
			s.Bedrooms = text
			s.State = StatePayment
			return Reply{Text: "Какой способ оплаты планируете?", Options: []string{PaymentCash, PaymentInstallment, PaymentMortgage, PaymentTradeIn}, AdvanceTo: StatePayment}
		}
		return Reply{Text: "Пожалуйста, выберите количество спален", Options: []string{Bedrooms1, Bedrooms2, Bedrooms3Plus}}

	case StatePayment:
		if text == PaymentCash || text == PaymentInstallment || text == PaymentMortgage || text == PaymentTradeIn {
			s.Payment = text
			s.State = StateFinalMessage
			return Reply{Text: "Где вам комфортнее получить подборку?", Options: []string{ChannelWhatsApp, ChannelExpertCall}, AdvanceTo: StateFinalMessage}
		}
		return Reply{Text: "Пожалуйста, выберите способ оплаты", Options: []string{PaymentCash, PaymentInstallment, PaymentMortgage, PaymentTradeIn}}

	case StateFinalMessage:
		if text == ChannelWhatsApp {
			return Reply{Text: finalTextForChannel(text), RemoveKeyboard: true, AdvanceTo: StateFinalMessage}
		}
		if text == ChannelExpertCall {
			// Сначала отправляем длинное пояснение, затем просим номер
			s.State = StateRequestPhone
			msg := finalTextForChannel(ChannelExpertCall) + "\n\n" + "Оставьте, пожалуйста, номер телефона — удобнее всего нажать кнопку ниже."
			return Reply{Text: msg, AdvanceTo: StateRequestPhone}
		}
		return Reply{Text: "Пожалуйста, выберите канал", Options: []string{ChannelWhatsApp, ChannelExpertCall}}
	}

	return Reply{Text: "Не понял команду"}
}

func finalTextForChannel(channel string) string {
	switch channel {
	case ChannelWhatsApp:
		return "Подготовим персональную подборку квартир в проектах Альянс‑Менеджмент и отправим её вам в Telegram."
	case ChannelExpertCall:
		return "Подготовим персональную подборку квартир в проектах Альянс‑Менеджмент и расчёты по ипотеке/рассрочке с учётом актуальных акций. Все консультации бесплатны 🔥 Оставьте номер — наш эксперт свяжется с вами. Если неудобно говорить по телефону, можем продолжить в Telegram."
	default:
		return "Спасибо! Наш эксперт свяжется с вами."
	}
}
