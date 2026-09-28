package ai

import (
	"bytes"
	"context"
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type Input struct {
	Opening            bool            `json:"opening,omitempty"`
	State              game.State      `json:"worldState"`
	Recent             []string        `json:"recentEvents"`
	PlayerID           int64           `json:"playerId"`
	Text               string          `json:"playerAction"`
	Results            []game.Result   `json:"engineResults,omitempty"`
	Correction         *PlanCorrection `json:"planCorrection,omitempty"`
	ResponseCorrection string          `json:"responseCorrection,omitempty"`
}
type PlanCorrection struct {
	Actions []game.Action `json:"rejectedActions"`
	Reason  string        `json:"reason"`
}
type Output struct {
	Narrative string        `json:"narrative"`
	Actions   []game.Action `json:"actions"`
	Memory    []string      `json:"memory"`
}
type Provider interface {
	GenerateTurn(context.Context, string, Input) (Output, error)
}
type Ollama struct {
	URL           string
	Client        *http.Client
	ContextWindow int
}

func New(url string) *Ollama {
	return &Ollama{URL: strings.TrimRight(url, "/"), Client: &http.Client{Timeout: 120 * time.Second}, ContextWindow: DefaultContextWindow}
}
func Prompt(in Input) string {
	p := `Ты Game Master. Мир и стиль задают worldState.settings. Сюжет — по правилам владельца, механику считает сервер. Пиши по-русски кратко: обычный ход 1–2 коротких абзаца, 2–6 предложений. Дай последствия и новую информацию, не повторяй сцену и действие, не решай за героя. Учитывай worldState и recentEvents. gmNotes — тайные зацепки, раскрывай их через игру. В memory записывай лишь новые факты о мире и NPC, не о героях и их вещах. Заметки не заменяют actions. Сначала выбери actions, потом narrative. Верни один JSON: actions, narrative, memory.`
	p += fmt.Sprintf(" narrative — максимум %d символов; это потолок, а не желаемая длина.", narrativeMaxChars(in))
	if in.State.Scene != nil {
		p += fmt.Sprintf(" ТЕКУЩАЯ локация героя — %q. recentEvents могут описывать прошлые места. Не описывай переход или действия в другом месте без MOVE_SCENE; подход к человеку или предмету внутри сцены не меняет локацию.", in.State.Scene.Location)
	}
	if in.Results != nil {
		p += ` Механика УЖЕ выполнена: actions=[]. Опиши engineResults, обычно 1 короткий абзац. Не повторяй броски и не меняй результаты. TURN_CHANGED указывает следующего игрока. PROPOSE_QUEST_COMPLETION — лишь предложение с причиной: цель активна, игрок может подтвердить итог или продолжить. GAME_FINISHED требует краткого эпилога; иначе не объявляй финал.`
	} else if in.Opening {
		p += ` Opening: максимум 2–3 коротких абзаца, без длинного пролога. Сам придумай место, ситуацию и ясную достижимую главную цель из сеттинга и worldDescription владельца. Цель сформулируй как проверяемое действие с понятным результатом и первой зацепкой: игрок должен знать, что ищет и зачем. Это конечная задача короткой кампании, а не первый шаг бесконечной цепочки; назови её игрокам во вступлении. Тайны оставь на пути к цели. Не превращай сеттинг в другой жанр и не решай за героя до первого действия. В memory запиши 1–3 скрытые зацепки или мотивы NPC, не добавляя вещи героям. Заверши вопросом «Что вы делаете?». actions: сначала MOVE_SCENE с name и description, затем один CREATE_QUEST с name и description; ещё допустимы до двух CREATE_NPC с name, description, status=friendly|neutral. Других действий нет.`
		if in.Correction != nil {
			p += ` Предыдущий план отклонён. Исправь его по planCorrection: обязательны сцена MOVE_SCENE и цель CREATE_QUEST в actions.`
		}
	} else {
		p += ` План: максимум 4 actions; без механики actions=[]. Типы и параметры:
ATTACK target=ID живого враждебного NPC текущей сцены, только в бою;
SKILL_CHECK skill=strength|dexterity|constitution|intelligence|wisdom|charisma dc=5..25;
DICE_ROLL — только самостоятельный бросок по просьбе игрока, name — dice notation (например, 1d20, 2d6+3). Для действия с характеристикой нужен SKILL_CHECK; обычное взаимодействие — actions=[].
CREATE_NPC name description status=hostile|friendly|neutral;
UPDATE_NPC target description;
SET_DISPOSITION target status=hostile|friendly|neutral (живой NPC текущей сцены);
MOVE_SCENE name description (переход в другую локацию, вне боя);
START_COMBAT (есть враждебный NPC); END_COMBAT (врагов не осталось);
CREATE_QUEST name description; UPDATE_QUEST target status=FAILED (в учебном «Последнем фонаре» также COMPLETED);
PROPOSE_QUEST_COMPLETION target=ID активной цели description=одна конкретная причина, подтверждающая выполнение цели; только пользовательская кампания вне боя;
ADD_ITEM name description (сюжетный предмет действующему герою); REMOVE_ITEM target=ID предмета его инвентаря.
За ход одна атака ИЛИ проверка ИЛИ бросок. ATTACK и SKILL_CHECK уже бросают кубики, DICE_ROLL к ним не добавляй. SKILL_CHECK идёт первым: остальные actions выполняются лишь при успехе. Наблюдение — без атаки, при необходимости SKILL_CHECK wisdom. Перемещение внутри комнаты обычно actions=[].
Появление NPC, переход и получение предмета в тексте ОБЯЗАТЕЛЬНО отражай соответствующим action. target — только ID из worldState. Если NPC есть лишь в тексте, сначала CREATE_NPC, взаимодействие по ID — в следующем ходе. Не воскрешай NPC и не меняй завершённые квесты. Для нападения на мирного NPC сначала SET_DISPOSITION hostile и START_COMBAT. Для примирения в бою сначала SKILL_CHECK charisma, затем SET_DISPOSITION neutral. Если врагов не осталось, сервер сам закончит бой. Не начинай бой при мирном разговоре. Не требуй проверку для обычного пути или очевидной подсказки; после провала предложи другой подход к цели.
combatOrder/combatIndex задают очередь; сервер передаёт ход сам. Прошлые броски не повторяй. Если предмет у другого героя, предложи ему выполнить действие. gmNotes и подсказки — варианты, а не обязательный маршрут: принимай разумные альтернативные действия игрока. Не закрывай цель из-за количества ходов или случайного движения и не создавай новую цель только ради продления. Если результат активной цели уже показан в текущем состоянии или действии игрока, ОБЯЗАТЕЛЬНО предложи PROPOSE_QUEST_COMPLETION с конкретной причиной. Иначе продолжай путь. Игрок подтвердит или продолжит. Пока нет подтверждения, квест и кампания активны, не пиши эпилог и не называй их завершёнными.`
		p += campaignPacing(in)
		if in.Correction != nil {
			p += ` План отклонён ДО механики. Исправь полный ответ для того же playerAction согласно planCorrection, сохрани намерение игрока. Не добавляй лишних действий.`
			for _, a := range in.Correction.Actions {
				if a.Type == "DICE_ROLL" {
					p += ` Пересмотри DICE_ROLL: исправь name на dice notation только если нужен самостоятельный случайный бросок; иначе удали его (actions=[]) или замени на SKILL_CHECK, если нужна характеристика. Сохранять DICE_ROLL не обязательно.`
					break
				}
			}
		}
		if in.State.Settings.LastLantern() {
			p += ` Для «Последнего фонаря»: огненный камень — один конкретный предмет с точным именем «Огненный камень». Не выдавай второй камень, пока первый у отряда. Не запрашивай UPDATE_QUEST FAILED после неудачного разговора или проверки: у игроков остаются другие способы. Нельзя закрывать исходный квест словами: герой должен ранее получить камень через ADD_ITEM, вернуться в сцену у моста и установить его через REMOVE_ITEM target=ID своего камня, затем UPDATE_QUEST COMPLETED. После проверенной установки сервер завершит кампанию. Если игрок явно нападает на похитителя, сервер запускает бой; мирный разговор сам по себе боя не требует.`
		}
	}
	if in.ResponseCorrection != "" {
		p += ` Предыдущий ответ не прошёл проверку формата. Исправь согласно responseCorrection, сохрани playerAction, worldState и результаты engineResults. Верни короткий полный JSON по схеме, ошибку игроку не цитируй.`
	}
	return p
}

func campaignPacing(in Input) string {
	if in.State.Settings.LastLantern() || len(in.State.Quests) == 0 {
		return ""
	}
	active, failed := false, false
	for _, quest := range in.State.Quests {
		active = active || quest.Status == "ACTIVE"
		failed = failed || quest.Status == "FAILED"
	}
	if !active {
		if failed {
			return " Цели исчерпаны; не начинай новую цепочку без явного выбора игроков."
		}
		return " Активных целей нет. Предложи владельцу подвести итоги кампании; не создавай новую цель без выбора игроков."
	}
	switch {
	case in.State.Turn >= 8:
		return " Веди к главной цели: дай явную зацепку или ближайший шаг через известные факты. Не затягивай путь, но не объявляй успех без действия игроков и проверяемого результата."
	case in.State.Turn >= 4:
		return " Веди к главной цели: покажи конкретный путь или препятствие. Любое разумное решение игрока может продвинуть сюжет."
	default:
		return " Веди к главной цели: за разумное действие давай полезную зацепку; после неудачи покажи иной путь. Не навязывай выбор."
	}
}
func (o *Ollama) GenerateTurn(ctx context.Context, model string, in Input) (Output, error) {
	var out Output
	window := o.ContextWindow
	if window == 0 {
		window = DefaultContextWindow
	}
	prepared, err := prepareInput(in, window)
	if err != nil {
		return out, err
	}
	in = prepared
	data, _ := json.Marshal(in)
	schema := responseSchema(in)
	body, _ := json.Marshal(struct {
		Model    string              `json:"model"`
		Stream   bool                `json:"stream"`
		Think    bool                `json:"think"`
		Format   json.RawMessage     `json:"format"`
		Messages []map[string]string `json:"messages"`
		Options  struct {
			NumCtx     int `json:"num_ctx"`
			NumPredict int `json:"num_predict"`
		} `json:"options"`
	}{model, false, false, schema, []map[string]string{{"role": "system", "content": Prompt(in)}, {"role": "user", "content": string(data)}}, struct {
		NumCtx     int `json:"num_ctx"`
		NumPredict int `json:"num_predict"`
	}{window, outputTokens(in)}})
	req, e := http.NewRequestWithContext(ctx, "POST", o.URL+"/api/chat", bytes.NewReader(body))
	if e != nil {
		return out, e
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := o.Client.Do(req)
	if e != nil {
		return out, requestError(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		kind := ErrRejected
		switch {
		case res.StatusCode == http.StatusNotFound:
			kind = ErrModelUnavailable
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
			kind = ErrBusy
		case res.StatusCode >= 500:
			kind = ErrUnavailable
		}
		return out, fmt.Errorf("%w (HTTP %d)", kind, res.StatusCode)
	}
	var envelope struct {
		Error   string `json:"error"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	payload, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil {
		return out, requestError(e)
	}
	if len(payload) > 1<<20 || json.Unmarshal(payload, &envelope) != nil {
		return out, invalidResponse("нужен один полный JSON-ответ в пределах лимита размера")
	}
	if envelope.Error != "" {
		return out, fmt.Errorf("%w: error envelope", ErrUnavailable)
	}
	dec := json.NewDecoder(strings.NewReader(envelope.Message.Content))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&out); e != nil {
		return Output{}, invalidResponse("нужен JSON по заданной схеме без markdown и неизвестных полей")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Output{}, invalidResponse("нужен ровно один JSON-объект без дополнительного текста")
	}
	if out.Actions == nil || out.Memory == nil {
		return Output{}, invalidResponse("обязательны массивы actions и memory, даже если они пустые")
	}
	if utf8.RuneCountInString(out.Narrative) > narrativeMaxChars(in) {
		return Output{}, invalidResponse(fmt.Sprintf("narrative слишком длинный: максимум %d символов; сократи текст и верни полный JSON", narrativeMaxChars(in)))
	}
	if len(out.Actions) > 4 || len(out.Memory) > 10 {
		return Output{}, invalidResponse("ответ слишком большой: максимум 4 действия и 10 фактов памяти")
	}
	for _, m := range out.Memory {
		if len(m) > 500 {
			return Output{}, invalidResponse("каждый факт memory должен быть не длиннее 500 байт")
		}
	}
	if in.Results != nil && len(out.Actions) > 0 {
		return Output{}, invalidResponse("механика уже выполнена: actions должен быть пустым")
	}
	if strings.TrimSpace(out.Narrative) == "" {
		return Output{}, invalidResponse("нужно непустое повествование narrative")
	}
	return out, nil
}
