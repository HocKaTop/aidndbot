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
	BeforeState        *game.State     `json:"-"` // Server-side evidence; never sent to the model.
	Opening            bool            `json:"opening,omitempty"`
	State              game.State      `json:"worldState"`
	Recent             []string        `json:"recentEvents"`
	PlayerID           int64           `json:"playerId"`
	Text               string          `json:"playerAction"`
	Intent             *PlayerIntent   `json:"playerIntent,omitempty"`
	Results            []game.Result   `json:"engineResults,omitempty"`
	Correction         *PlanCorrection `json:"planCorrection,omitempty"`
	ResponseCorrection string          `json:"responseCorrection,omitempty"`
}
type PlayerIntent struct {
	SceneChange   bool     `json:"sceneChange"`
	ItemRequest   string   `json:"itemRequest,omitempty"`
	GiveItemID    string   `json:"giveItemId,omitempty"`
	HeldItemID    string   `json:"heldItemId,omitempty"`
	ItemName      string   `json:"itemName,omitempty"`
	CompanionNPCs []string `json:"companionNPCs,omitempty"`
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

// A home Ollama instance can be overwhelmed when several rooms act together.
// Count HTTP requests, including narration and corrections, across providers.
var ollamaRequests = make(chan struct{}, 2)

func New(url string) *Ollama {
	return &Ollama{URL: strings.TrimRight(url, "/"), Client: &http.Client{Timeout: 120 * time.Second}, ContextWindow: DefaultContextWindow}
}
func Prompt(in Input) string {
	p := `Ты Game Master. Мир и стиль задают worldState.settings. Сюжет — по правилам владельца, механику считает сервер. Пиши по-русски кратко: обычный ход 1–2 коротких абзаца, 2–6 предложений. Дай последствия и новую информацию, не повторяй сцену и действие, не решай за героя. Учитывай worldState и recentEvents. playerHistory — намерения игроков; факты мира — state и summary. gmNotes — тайные зацепки, раскрывай их через игру. В memory записывай лишь новые факты о мире и NPC, не о героях и их вещах. Заметки не заменяют actions. Сначала выбери actions, потом narrative. Верни один JSON: actions, narrative, memory.`
	p += fmt.Sprintf(" narrative — максимум %d символов; это потолок, а не желаемая длина.", narrativeMaxChars(in))
	if in.State.Scene != nil {
		p += fmt.Sprintf(" Текущая локация — %q. playerIntent.sceneChange=false запрещает переход. Прибытие в новое место требует MOVE_SCENE; в известное — REVISIT_SCENE по ID. Подход к объекту внутри сцены не меняет локацию.", in.State.Scene.Location)
	}
	if in.Results != nil {
		p += ` Механика УЖЕ выполнена: actions=[]. Опиши engineResults, обычно 1 короткий абзац. Предметы и местоположение NPC — строго worldState: проверка без ADD_ITEM не выдала вещь; без REMOVE_ITEM её не передали; без MOVE_NPC персонаж не ушёл. Не повторяй броски. Предложения исхода цели не завершают её до подтверждения. GAME_FINISHED требует эпилога; иначе финала нет.`
	} else if in.Opening {
		p += ` Сохрани прямо заданную конечную цель владельца и её условия. Среди CREATE_NPC приоритет у названного участника главной цели, который находится в сцене; не заменяй его второстепенными NPC.`
		p += ` Opening: максимум 2–3 коротких абзаца, без длинного пролога. Сам придумай место, ситуацию и ясную достижимую главную цель из сеттинга и worldDescription владельца. Цель сформулируй как проверяемое действие с понятным результатом и первой зацепкой: игрок должен знать, что ищет и зачем. Это конечная задача короткой кампании, а не первый шаг бесконечной цепочки; назови её игрокам во вступлении. Тайны оставь на пути к цели. Не превращай сеттинг в другой жанр и не решай за героя до первого действия. В memory запиши 1–3 скрытые зацепки или мотивы NPC, не добавляя вещи героям. Заверши вопросом «Что вы делаете?». actions: сначала MOVE_SCENE с name и description, затем один CREATE_QUEST с name и description; ещё допустимы до двух CREATE_NPC с name, description, status=friendly|neutral. Других действий нет.`
		if in.Correction != nil {
			p += ` Предыдущий план отклонён. Исправь его по planCorrection: обязательны сцена MOVE_SCENE и цель CREATE_QUEST в actions.`
		}
	} else {
		if in.Intent != nil {
			if len(in.Intent.CompanionNPCs) > 0 {
				p += ` playerIntent.companionNPCs идут с героем: при переходе перемести их через MOVE_NPC name=@current. При отказе спутника не описывай совместный переход.`
			}
			switch {
			case in.Intent.ItemRequest != "":
				p += ` playerIntent.itemRequest — вещь, которую герой просит получить. При согласии нужен ADD_ITEM; риск: SKILL_CHECK первым, ADD_ITEM после него. actions=[] означает отказ без передачи, не обещай уже полученную вещь.`
			case in.Intent.GiveItemID != "":
				p += ` Герой передаёт playerIntent.giveItemId: при передаче нужен REMOVE_ITEM с этим ID. Отказ — actions=[] без передачи.`
			case in.Intent.HeldItemID != "":
				p += ` playerIntent.heldItemId уже у героя: не создавай вторую копию и не повторяй получение.`
			}
		}
		p += ` План: максимум 4 actions; без механики actions=[]. Типы и параметры:
ATTACK target=ID живого враждебного NPC текущей сцены, только в бою;
SKILL_CHECK skill=strength|dexterity|constitution|intelligence|wisdom|charisma dc=5..25;
DICE_ROLL — только самостоятельный бросок по просьбе игрока, name — dice notation (например, 1d20, 2d6+3). Для действия с характеристикой нужен SKILL_CHECK; обычное взаимодействие — actions=[].
CREATE_NPC name description status=hostile|friendly|neutral threat=minor|standard|elite (сильный враг — elite);
UPDATE_NPC target description;
MOVE_NPC target=ID NPC name=ID или название места, @current — текущая сцена после перехода;
SET_DISPOSITION target status=hostile|friendly|neutral (живой NPC текущей сцены);
MOVE_SCENE name description (новое место, вне боя);
CREATE_LOCATION name description (открыть место без перехода отряда, затем NPC может уйти туда через MOVE_NPC);
REVISIT_SCENE target=ID из worldState.locations (известное место, вне боя);
RECORD_LOCATION_FACT description=новый устойчивый факт текущего места;
START_COMBAT (есть враждебный NPC); END_COMBAT (врагов не осталось);
CREATE_QUEST name description; UPDATE_QUEST target status=COMPLETED только в учебном «Последнем фонаре»;
PROPOSE_QUEST_COMPLETION target=ID активной цели description=одна конкретная причина, подтверждающая выполнение цели; только пользовательская кампания вне боя;
PROPOSE_QUEST_FAILURE target=ID активной цели description=необратимая причина провала, подтверждённая действиями игроков; только пользовательская кампания вне боя. После одной неудачной проверки предлагай иной путь, а не провал;
ADD_ITEM name description (сюжетный предмет действующему герою); REMOVE_ITEM target=ID предмета его инвентаря.
За ход одна атака ИЛИ проверка ИЛИ бросок. ATTACK и SKILL_CHECK уже бросают кубики, DICE_ROLL к ним не добавляй. SKILL_CHECK идёт первым: остальные actions выполняются лишь при успехе. Перемещение внутри комнаты обычно actions=[].
Появление NPC, переход и получение вещи требуют action. target — ID из worldState. CREATE_NPC — новый персонаж здесь, не слухи/следы и не вещь; вещь — ADD_ITEM. Известного NPC перемещай через MOVE_NPC, не создавай повторно. Не воскрешай NPC и не меняй закрытые цели. Нападение на мирного NPC: SET_DISPOSITION hostile, START_COMBAT. Примирение в бою: SKILL_CHECK charisma, SET_DISPOSITION neutral. Сервер сам завершает бой без врагов и передаёт ход по combatOrder/combatIndex. Мирный разговор не начинает бой. Обычный путь и очевидная подсказка не требуют проверки; после провала дай иной подход.
Предмет другого героя использует его владелец. gmNotes — зацепки, принимай разумные альтернативы. Не закрывай цель по числу ходов и не создавай новую ради продления. Если результат цели уже показан в состоянии или действии игрока, предложи PROPOSE_QUEST_COMPLETION с конкретной причиной. Иначе продолжай путь. До подтверждения игроком цель и кампания активны: без эпилога и заявлений о завершении.`
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
			p += ` «Последний фонарь»: одна цель, новых квестов нет. Камень у похитителя на мельнице: не передавай его новым духам или держателям в прозе. Уговор — SKILL_CHECK charisma; согласие отдать камень — SET_DISPOSITION friendly, затем ADD_ITEM с точным именем «Огненный камень». Не выдавай второй камень. Провал проверки не закрывает цель: дай иной путь. Для финала герой получает камень, возвращается к мосту, устанавливает через REMOVE_ITEM target=ID камня, затем UPDATE_QUEST COMPLETED. Сервер завершит кампанию. Нападение запускает бой, мирный разговор — нет.`
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
			NumCtx      int     `json:"num_ctx"`
			NumPredict  int     `json:"num_predict"`
			Temperature float64 `json:"temperature"`
		} `json:"options"`
	}{model, false, false, schema, []map[string]string{{"role": "system", "content": Prompt(in)}, {"role": "user", "content": string(data)}}, struct {
		NumCtx      int     `json:"num_ctx"`
		NumPredict  int     `json:"num_predict"`
		Temperature float64 `json:"temperature"`
	}{window, outputTokens(in), 0.2}})
	req, e := http.NewRequestWithContext(ctx, "POST", o.URL+"/api/chat", bytes.NewReader(body))
	if e != nil {
		return out, e
	}
	req.Header.Set("Content-Type", "application/json")
	select {
	case ollamaRequests <- struct{}{}:
		defer func() { <-ollamaRequests }()
	case <-ctx.Done():
		return out, requestError(ctx.Err())
	}
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
