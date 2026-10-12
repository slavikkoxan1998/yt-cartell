package codeanim

// The animation grammars: the 8 shipped with the upstream engine
// (engine/clips/<id>.js) and any registered by other packages
// (RegisterGrammar). Metadata is a Ukrainian digest of the upstream contract
// (references/09-视频动画语法.md «片段契约» and the grammar cards
// references/动画语法/*.md) — enough for a planner (human or LLM) to write a
// spec that renders.

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Grammar describes one animation grammar (an id of spec.grammar).
type Grammar struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`           // Ukrainian name for the UI
	Description string   `json:"description"`     // one or two sentences: what the picture looks like
	Uses        []string `json:"uses"`            // when to pick it (Ukrainian)
	Avoid       string   `json:"avoid,omitempty"` // when not to
	// Data are the spec-level fields of spec.data the grammar reads.
	Data []Field `json:"data,omitempty"`
	// Cues are the cue kinds it draws (other kinds draw nothing).
	Cues []CueKind `json:"cues"`
	// Example is a minimal spec that renders (copy and change the words).
	Example json.RawMessage `json:"example"`
	// Schema is a JSON Schema of the whole spec for this grammar; Grammars()
	// generates it from Data and Cues when empty.
	Schema json.RawMessage `json:"schema,omitempty"`
	// Reference is the grammar's full spec reference for a spec writer
	// (markdown; "" = Data and Cues say everything).
	Reference string `json:"-"`
	// Examples are complete specs that render well (beyond Example); the
	// image paths in them are placeholders.
	Examples []json.RawMessage `json:"-"`
}

// Field is one parameter. Name is the JSON key ("data.index" for a key
// inside the cue's data object); Type is a JSON type ("string", "number",
// "integer", "boolean", "array", "object"; alternatives with "|").
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Doc  string `json:"doc"`
}

// CueKind is one kind of cue a grammar understands.
type CueKind struct {
	Kind    string          `json:"kind"`
	Aliases []string        `json:"aliases,omitempty"` // other kinds drawn the same way
	Fields  []Field         `json:"fields,omitempty"`  // besides at and kind
	Doc     string          `json:"doc"`
	Example json.RawMessage `json:"example,omitempty"`
}

var registry struct {
	mu    sync.RWMutex
	meta  map[string]Grammar
	js    map[string][]byte // registered (non-embedded) grammar code
	order []string
}

func init() {
	registry.meta = map[string]Grammar{}
	registry.js = map[string][]byte{}
	for _, g := range builtinGrammars {
		registry.meta[g.ID] = g
		registry.order = append(registry.order, g.ID)
	}
}

// RegisterGrammar adds (or replaces) a grammar: js is its clip file — it
// must register CLIPS[id] = { draw(c, t, ctx), init?, fonts?, safe? } like
// engine/clips/*.js — and meta describes it (meta.ID is forced to id).
// Safe to call from init functions and concurrently with renders.
func RegisterGrammar(id string, js []byte, meta Grammar) {
	id = strings.TrimSpace(id)
	if !validID(id) {
		panic("codeanim: bad grammar id " + id)
	}
	meta.ID = id
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, ok := registry.meta[id]; !ok {
		registry.order = append(registry.order, id)
	}
	registry.meta[id] = meta
	registry.js[id] = append([]byte(nil), js...)
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// Grammars lists every grammar (the 8 built-in first, then registered
// ones), each with Schema filled.
func Grammars() []Grammar {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	out := make([]Grammar, 0, len(registry.order))
	for _, id := range registry.order {
		g := registry.meta[id]
		if len(g.Schema) == 0 {
			g.Schema = schemaFor(g)
		}
		out = append(out, g)
	}
	return out
}

// GrammarByID returns one grammar's metadata.
func GrammarByID(id string) (Grammar, bool) {
	registry.mu.RLock()
	g, ok := registry.meta[id]
	registry.mu.RUnlock()
	if ok && len(g.Schema) == 0 {
		g.Schema = schemaFor(g)
	}
	return g, ok
}

// grammarJS returns the clip file of a grammar (registered code first, then
// the embedded engine/clips/<id>.js).
func grammarJS(id string) ([]byte, bool) {
	if !validID(id) {
		return nil, false
	}
	registry.mu.RLock()
	js, ok := registry.js[id]
	_, known := registry.meta[id]
	registry.mu.RUnlock()
	if ok {
		return js, true
	}
	if !known {
		return nil, false
	}
	b, err := readEngine("clips/" + id + ".js")
	return b, err == nil
}

func knownGrammar(id string) bool {
	_, ok := grammarJS(id)
	return ok
}

// ---- JSON Schema ---------------------------------------------------------------------------

func jsonType(t string) any {
	var ts []string
	for _, p := range strings.Split(t, "|") {
		switch p = strings.TrimSpace(p); p {
		case "string", "number", "integer", "boolean", "array", "object", "null":
			ts = append(ts, p)
		}
	}
	switch len(ts) {
	case 0:
		return nil
	case 1:
		return ts[0]
	}
	return ts
}

func prop(f Field) map[string]any {
	p := map[string]any{"description": f.Doc}
	if t := jsonType(f.Type); t != nil {
		p["type"] = t
	}
	return p
}

func schemaFor(g Grammar) json.RawMessage {
	dataProps := map[string]any{}
	for _, f := range g.Data {
		dataProps[f.Name] = prop(f)
	}
	var kinds []string
	cueProps := map[string]any{}
	cueData := map[string]any{}
	var kindDocs []string
	for _, c := range g.Cues {
		kinds = append(kinds, c.Kind)
		kinds = append(kinds, c.Aliases...)
		kindDocs = append(kindDocs, c.Kind+": "+c.Doc)
		for _, f := range c.Fields {
			if n, ok := strings.CutPrefix(f.Name, "data."); ok {
				if _, dup := cueData[n]; !dup {
					cueData[n] = prop(Field{Name: n, Type: f.Type, Doc: "(" + c.Kind + ") " + f.Doc})
				}
				continue
			}
			if _, dup := cueProps[f.Name]; !dup {
				cueProps[f.Name] = prop(f)
			}
		}
	}
	sort.Strings(kinds)
	kinds = dedup(kinds)
	cueProps["at"] = map[string]any{"type": "number", "minimum": 0, "description": "Секунда, коли елемент уперше видно (кадр «розкриття»); для number — момент, коли число завмирає."}
	cueProps["kind"] = map[string]any{"type": "string", "enum": kinds, "description": strings.Join(kindDocs, "\n")}
	if len(cueData) > 0 {
		cueProps["data"] = map[string]any{"type": "object", "properties": cueData}
	}
	s := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"title":       g.ID + " — " + g.Label,
		"description": g.Description,
		"type":        "object",
		"required":    []string{"grammar", "duration", "cues"},
		"properties": map[string]any{
			"grammar":  map[string]any{"const": g.ID},
			"duration": map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "Тривалість кліпу, с (кадрів = round(duration×fps))."},
			"fps":      map[string]any{"type": "number", "default": 30},
			"width":    map[string]any{"type": "integer", "default": 1920},
			"height":   map[string]any{"type": "integer", "default": 1080, "description": "Вертикальне відео: width 1080, height 1920."},
			"safe": map[string]any{"type": "object", "description": "Поля (px), куди не ставити текст: місце для субтитрів/інтерфейсу платформи. Працюють top і bottom; fill — колір цих полів.",
				"properties": map[string]any{"top": map[string]any{"type": "number"}, "bottom": map[string]any{"type": "number"}, "left": map[string]any{"type": "number"}, "right": map[string]any{"type": "number"}, "fill": map[string]any{"type": "string"}}},
			"alpha": map[string]any{"type": "boolean", "description": "Без фону (рендер із прозорістю ставить сам)."},
			"data":  map[string]any{"type": "object", "properties": dataProps},
			"cues": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"at", "kind"}, "properties": cueProps,
			}},
		},
	}
	b, _ := json.Marshal(s)
	return b
}

func dedup(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// ---- the built-in eight ----------------------------------------------------------------------

var (
	fText  = Field{"text", "string", "Основний текст."}
	fDur   = Field{"dur", "number", "Тривалість анімації появи, с."}
	fImage = Field{"image", "string", "Шлях до зображення (відносно теки ресурсів або абсолютний; data: теж можна). Вписується без обрізання."}
	numCue = CueKind{
		Kind: "number",
		Fields: []Field{
			{"text", "string", "Підпис до числа (text, sub і data.text — одне й те саме)."},
			{"data.value", "number", "Значення; лічильник біжить від 0 до нього (від’ємні — зі справжнім мінусом)."},
			{"data.prefix", "string", "Префікс, напр. \"$\"."},
			{"data.suffix", "string", "Суфікс, напр. \"%\" або \" млн\" (пробіл пиши сам)."},
			{"data.decimals", "integer", "Знаків після коми (типово 0)."},
			{"data.label", "string", "Маленька мітка над числом (t2) або вгорі зліва (y5)."},
		},
		Doc:     "Велике число-лічильник. at — момент, коли число завмирає (рахунок стартує на 0,9 с раніше; у y5 — на 0,7 с), тож at ≥ 0,9.",
		Example: json.RawMessage(`{"at":4,"kind":"number","text":"активних користувачів щомісяця","data":{"value":1.2,"decimals":1,"suffix":" млрд"}}`),
	}
)

var builtinGrammars = []Grammar{
	{
		ID:          "t1_3b1b",
		Label:       "Математика (3Blue1Brown)",
		Description: "Темне тло, формули, що «пишуться», графіки, що перетікають один в одного, жовта рамка на головному — стиль 3Blue1Brown / manim.",
		Uses:        []string{"пояснити поняття чи механізм через формулу", "покроковий висновок", "крива, що змінюється (навчання, спад, ріст)"},
		Avoid:       "немає що перетворювати чи виводити — тоді краще y5 (кінетичний текст)",
		Cues: []CueKind{
			{Kind: "title", Fields: []Field{fText, {"data.color", "string", "Колір (назва manim-кольору або #hex)."}, fDur}, Doc: "Заголовок пишеться з at; новий заголовок витісняє старий угору.", Example: json.RawMessage(`{"at":0,"kind":"title","text":"Увага — це зважене середнє"}`)},
			{Kind: "equation", Fields: []Field{{"text", "string", "Формула текстом (одиночні літери — курсивом)."}, {"data.tokens", "array", "Точніше: [{s, it?, col?, gap?, sub?, sup?, size?}], col — BLUE_C, YELLOW, GREEN_C, TEAL_C, RED_C… або #hex."}, fDur}, Doc: "Формула; кожна наступна перетікає з попередньої.", Example: json.RawMessage(`{"at":1.2,"kind":"equation","text":"softmax(QK)V"}`)},
			{Kind: "point", Fields: []Field{fText, fDur}, Doc: "Теза одним рядком; попередні тьмяніють.", Example: json.RawMessage(`{"at":3,"kind":"point","text":"Спершу рахуємо схожість"}`)},
			{Kind: "line", Fields: []Field{{"text", "string", "Підпис кривої."}, {"data.values", "array", "Значення y через рівні кроки."}, {"data.points", "array", "Або точки [[x,y],…]."}, {"data.color", "string", "Колір кривої."}, fDur}, Doc: "Осі, потім крива; наступна крива перетікає з попередньої. З кривою: горизонтально текст ліворуч, графік праворуч.", Example: json.RawMessage(`{"at":2,"kind":"line","text":"Втрати","data":{"values":[9,5,3,2,1.5]}}`)},
			{Kind: "highlight", Fields: []Field{{"data.index", "integer", "Яку тезу (point) виділити, з 0; типово — останню."}, {"data.target", "string", "\"equation\" — виділити формулу."}}, Doc: "Пульс + жовта рамка, що лишається.", Example: json.RawMessage(`{"at":5,"kind":"highlight","data":{"index":0}}`)},
		},
		Example: json.RawMessage(`{"grammar":"t1_3b1b","duration":6,"cues":[{"at":0,"kind":"title","text":"Увага — це зважене середнє"},{"at":1.2,"kind":"equation","text":"softmax(QK)V"},{"at":3,"kind":"point","text":"Спершу рахуємо, наскільки слова схожі"},{"at":4.2,"kind":"point","text":"Потім складаємо інформацію з вагами"}]}`),
	},
	{
		ID:          "t2_keynote_ui",
		Label:       "Презентація (Keynote)",
		Description: "Світлий інтерфейс у стилі презентацій Apple: розмиті градієнти, скляні картки з іконками чи скриншотами, велике число.",
		Uses:        []string{"продукт, функції, параметри", "кроки інструкції зі скриншотами", "одне велике число"},
		Avoid:       "логіка й виведення (скло нічого не пояснює) — краще t1",
		Data: []Field{
			{"eyebrow", "string", "Дрібний надпис над заголовком."},
			{"title", "string", "Заголовок."},
			{"subtitle", "string", "Підзаголовок."},
			{"accent", "string", "Акцентний колір (типово #3d6bff)."},
		},
		Cues: []CueKind{
			{Kind: "title", Fields: []Field{{"text", "string", "Замінює data.title."}, {"sub", "string", "Замінює data.subtitle."}}, Doc: "Заголовок виринає з розмиття.", Example: json.RawMessage(`{"at":0,"kind":"title"}`)},
			{Kind: "card", Aliases: []string{"step", "image"}, Fields: []Field{{"text", "string", "Заголовок картки."}, {"sub", "string", "Пояснення."}, fImage, {"data.icon", "string", "Іконка картки без зображення: wave memory agent clock phone layers chart code check play bolt lock."}, {"data.step", "integer", "Номер кроку."}, {"data.value", "number", "Число-лічильник на картці."}, {"data.prefix", "string", "Префікс числа."}, {"data.suffix", "string", "Суфікс числа."}, {"data.decimals", "integer", "Знаків після коми."}, {"data.label", "string", "Підпис числа."}}, Doc: "Картка. Зі скриншотом — одна на екран; без — до 3 поспіль в один ряд.", Example: json.RawMessage(`{"at":2,"kind":"card","text":"Картинка з одного речення","sub":"без жодних промптів","data":{"icon":"bolt"}}`)},
			{Kind: "highlight", Fields: []Field{{"data.rect", "array", "[x,y,w,h] у частках скриншота (0..1)."}, {"text", "string", "Мітка (необов’язково)."}}, Doc: "Рамка на частині поточного скриншота, решта темніє.", Example: json.RawMessage(`{"at":5,"kind":"highlight","data":{"rect":[0.6,0.2,0.2,0.1]}}`)},
			numCue,
		},
		Example: json.RawMessage(`{"grammar":"t2_keynote_ui","duration":6,"data":{"title":"Три нові функції"},"cues":[{"at":0,"kind":"title"},{"at":1.8,"kind":"card","text":"Голосове введення","sub":"сказав — і вже текст","data":{"icon":"wave"}},{"at":2.6,"kind":"card","text":"Довга пам’ять","sub":"пам’ятає минулу розмову","data":{"icon":"memory"}},{"at":3.4,"kind":"card","text":"Автовиконання","sub":"натисне решту кнопок за тебе","data":{"icon":"agent"}}]}`),
	},
	{
		ID:          "t3_finance_chart",
		Label:       "Фінансовий графік (The Economist)",
		Description: "Біле тло, червоний акцент, стовпчики / лінія / свічки, що виростають, підпис джерела й одна позначка — графіки в стилі The Economist.",
		Uses:        []string{"цифри, ріст, тренд, порівняння", "ціни, курси, звітність"},
		Avoid:       "лише одне число — тоді велике число в t2 або y5",
		Data: []Field{
			{"title", "string", "Заголовок."},
			{"unit", "string", "Підзаголовок: показник і одиниці."},
			{"source", "string", "Джерело (ліворуч унизу)."},
			{"badge", "string", "Позначка праворуч унизу, напр. «умовні дані» (для справжніх даних не пиши)."},
			{"chart", "string", "\"bar\" | \"line\" | \"candle\"."},
			{"series", "array", "[{label, value, color?}] або для свічок [{label, o, h, l, c}]."},
			{"decimals", "integer", "Знаків після коми в підписах (типово 1)."},
			{"prefix", "string", "Префікс значень."},
			{"suffix", "string", "Суфікс значень."},
			{"highlight", "object", "{index, text, sub} — що позначити."},
			{"colors", "object", "{main, accent, dim}."},
			{"upDown", "string", "\"cn\" (червоний — ріст, типово) | \"us\" (зелений — ріст)."},
			{"xAxis", "string", "\"category\" | \"time\"."},
		},
		Cues: []CueKind{
			{Kind: "title", Doc: "Спершу макет: заголовок, підзаголовок, джерело.", Example: json.RawMessage(`{"at":0,"kind":"title"}`)},
			{Kind: "bar", Aliases: []string{"line", "candle"}, Fields: []Field{fDur, {"data.index", "integer|array", "Для стовпчиків: які саме виростають у цей момент."}}, Doc: "Осі → дані виростають. Стовпчики завжди від 0.", Example: json.RawMessage(`{"at":1.2,"kind":"bar"}`)},
			{Kind: "highlight", Fields: []Field{{"text", "string", "Замінює data.highlight.text."}, {"sub", "string", "Замінює data.highlight.sub."}, {"data.index", "integer", "Який стовпчик/точку."}}, Doc: "Точка-позначка з виноскою.", Example: json.RawMessage(`{"at":4,"kind":"highlight","data":{"index":2},"text":"максимум"}`)},
			numCue,
		},
		Example: json.RawMessage(`{"grammar":"t3_finance_chart","duration":6,"data":{"title":"Витрати на дослідження","unit":"2024 рік, млн доларів","source":"Джерело: умовні дані","badge":"умовні дані","chart":"bar","series":[{"label":"А","value":120},{"label":"Б","value":86},{"label":"В","value":43}]},"cues":[{"at":0,"kind":"title"},{"at":1,"kind":"bar"},{"at":3.5,"kind":"highlight","data":{"index":0},"text":"найбільше"}]}`),
	},
	{
		ID:          "y1_kurzgesagt",
		Label:       "Науч-поп (Kurzgesagt)",
		Description: "Пласка яскрава графіка: планета-центр із вузлами-супутниками, наближення камери й «занурення» на рівень глибше — стиль Kurzgesagt.",
		Uses:        []string{"що всередині системи, з чого вона складається", "масштаб: від великого до дрібного", "послідовність етапів (data.flow)"},
		Avoid:       "новини з реальними скриншотами — мультяшність знижує довіру; краще y2",
		Data: []Field{
			{"center", "string", "Назва центральної «планети» першого рівня."},
			{"flow", "boolean", "true — вузли з’єднані в ланцюг з номерами (коли важливий порядок)."},
		},
		Cues: []CueKind{
			{Kind: "title", Fields: []Field{fText}, Doc: "Заголовок угорі; зникає з першим highlight чи enter.", Example: json.RawMessage(`{"at":0,"kind":"title","text":"Що відбувається під час пошуку"}`)},
			{Kind: "point", Fields: []Field{fText, {"data.color", "string", "Колір вузла."}}, Doc: "Вузол-супутник поточного рівня з підписом (≤3 — півколом, ≥4 — колом).", Example: json.RawMessage(`{"at":1.4,"kind":"point","text":"Індексація"}`)},
			{Kind: "highlight", Fields: []Field{{"data.index", "integer", "Який вузол поточного рівня, з 0; типово — останній."}}, Doc: "Камера наближається до вузла, решта темніє (1,2 с).", Example: json.RawMessage(`{"at":5,"kind":"highlight","data":{"index":1}}`)},
			{Kind: "enter", Fields: []Field{{"data.index", "integer", "У який вузол «зануритися»."}, {"text", "string", "Назва нового центру (типово — назва вузла)."}, {"dur", "number", "Тривалість занурення (типово 1,6 с); наступні point — після at+dur."}}, Doc: "Перехід масштабу: камера пірнає у вузол, з’являється новий рівень.", Example: json.RawMessage(`{"at":4,"kind":"enter","data":{"index":0},"text":"Токенізатор"}`)},
		},
		Example: json.RawMessage(`{"grammar":"y1_kurzgesagt","duration":6,"data":{"center":"Пошуковик","flow":true},"cues":[{"at":0,"kind":"title","text":"Що відбувається під час пошуку"},{"at":1.2,"kind":"point","text":"Обхід"},{"at":2,"kind":"point","text":"Індекс"},{"at":2.8,"kind":"point","text":"Ранжування"},{"at":4.2,"kind":"highlight","data":{"index":2}}]}`),
	},
	{
		ID:          "y2_vox",
		Label:       "Колаж-розслідування (Vox)",
		Description: "Стіл-колаж: скриншоти й вирізки з паперовою рамкою та скотчем, червоні нитки, маркер і коло, камера наближається — стиль Vox.",
		Uses:        []string{"новини, події, історія з реальними скриншотами й цитатами", "докази й першоджерела"},
		Avoid:       "абстрактні механізми без матеріалів — вийде фальшивий архів; краще y1 чи t1",
		Cues: []CueKind{
			{Kind: "image", Aliases: []string{"clip"}, Fields: []Field{fImage, {"text", "string", "Чорна мітка під зображенням."}, {"data.halftone", "boolean", "Перетворити на сірий растр."}}, Doc: "Зображення в’їжджає на стіл (горизонтально зліва направо, вертикально згори вниз), нитки з’єднують.", Example: json.RawMessage(`{"at":1,"kind":"image","image":"screenshot.png","text":"Головна сторінка"}`)},
			{Kind: "title", Fields: []Field{fText}, Doc: "Чорна смуга з білим текстом, що друкується.", Example: json.RawMessage(`{"at":0,"kind":"title","text":"Три сліди однієї історії"}`)},
			{Kind: "point", Fields: []Field{fText}, Doc: "Картка-нотатка, текст друкується.", Example: json.RawMessage(`{"at":4,"kind":"point","text":"Анонс вийшов на день раніше"}`)},
			{Kind: "highlight", Fields: []Field{{"data.rect", "array", "[x,y,w,h] у частках поточного зображення (0..1)."}}, Doc: "Маркер → червоне коло → камера наближається.", Example: json.RawMessage(`{"at":2.5,"kind":"highlight","data":{"rect":[0.1,0.2,0.3,0.1]}}`)},
		},
		Example: json.RawMessage(`{"grammar":"y2_vox","duration":6,"cues":[{"at":0,"kind":"title","text":"Три сліди подорожчання"},{"at":1.6,"kind":"point","text":"Спершу постачальник розіслав лист"},{"at":3.4,"kind":"point","text":"За два тижні змінилися ціни на сайті"}]}`),
	},
	{
		ID:          "y3_whiteboard",
		Label:       "Біла дошка (RSA Animate)",
		Description: "Біла дошка: фраза пишеться від руки під голос, поруч малюються іконки зі стрілками, головне обводиться — стиль RSA Animate.",
		Uses:        []string{"міркування крок за кроком", "пояснення причин і наслідків", "лекція, урок"},
		Avoid:       "різка зміна всього екрана — дошка будується поступово",
		Cues: []CueKind{
			{Kind: "title", Fields: []Field{fText}, Doc: "Заголовок пишеться від руки (сам пришвидшується, якщо не встигає).", Example: json.RawMessage(`{"at":0,"kind":"title","text":"Навіщо писати тести"}`)},
			{Kind: "point", Fields: []Field{fText}, Doc: "Маркер-кружечок і рядок тексту.", Example: json.RawMessage(`{"at":1.5,"kind":"point","text":"Хтось стежить за кодом замість тебе"}`)},
			{Kind: "draw", Fields: []Field{{"data.icon", "string", "person cat network doc chat bulb check cross screen stack gear target clock chart."}, {"text", "string", "Підпис під іконкою."}, {"data.arrow", "boolean", "false — без стрілки до попередньої."}}, Doc: "Іконка поряд із щойно написаним рядком; сусідні draw з’єднуються стрілками.", Example: json.RawMessage(`{"at":2.5,"kind":"draw","text":"Автоперевірка","data":{"icon":"check"}}`)},
			{Kind: "image", Fields: []Field{fImage}, Doc: "Зображення відкривається зигзагом у мальованій рамці.", Example: json.RawMessage(`{"at":4,"kind":"image","image":"sketch.png"}`)},
			{Kind: "highlight", Fields: []Field{{"data.index", "integer", "Котрий елемент, з 0; типово — останній."}, {"data.target", "string", "\"title\" | \"draw\" | \"image\" (типово рахуються point)."}, {"data.style", "string", "\"underline\" — підкреслити замість обвести."}}, Doc: "Помаранчеве коло або підкреслення.", Example: json.RawMessage(`{"at":5,"kind":"highlight","data":{"index":0,"style":"underline"}}`)},
		},
		Example: json.RawMessage(`{"grammar":"y3_whiteboard","duration":6,"cues":[{"at":0,"kind":"title","text":"Навіщо писати тести"},{"at":1.5,"kind":"point","text":"Хтось стежить за кодом"},{"at":2.7,"kind":"draw","text":"Автоперевірка","data":{"icon":"check"}},{"at":4.4,"kind":"highlight","data":{"index":0}}]}`),
	},
	{
		ID:          "y4_storytime",
		Label:       "Історія з персонажем (storytime)",
		Description: "Простий мальований персонаж за столом розповідає: бульбашки реплік, крупний план реакції, вставка екрана комп’ютера з великим написом.",
		Uses:        []string{"особистий досвід, курйози, провали", "гумор, що тримається на паузі й реакції"},
		Avoid:       "презентація хороших новин — краще t2",
		Cues: []CueKind{
			{Kind: "point", Aliases: []string{"title"}, Fields: []Field{{"text", "string", "Репліка повністю (визначає, скільки персонаж говорить)."}, {"sub", "string", "Короткий текст у бульбашці (до ~12 символів; без нього — text)."}, {"data.pose", "string", "talk | point | shrug | type."}}, Doc: "Бульбашка з’являється, персонаж говорить.", Example: json.RawMessage(`{"at":0.1,"kind":"point","text":"Я попросив виправити одну літеру,","sub":"одну літеру!"}`)},
			{Kind: "highlight", Fields: []Field{{"data.mark", "string", "Знак над головою (типово «？»), напр. «!»."}}, Doc: "Знак питання/оклику і розведені руки.", Example: json.RawMessage(`{"at":4,"kind":"highlight","data":{"mark":"!"}}`)},
			{Kind: "react", Fields: []Field{{"dur", "number", "Скільки триває (типово до наступного cue)."}}, Doc: "Крупний план реакції: мертвий погляд і крапля поту, без слів.", Example: json.RawMessage(`{"at":3,"kind":"react"}`)},
			{Kind: "insert", Fields: []Field{{"sub", "string", "Що він набирає на комп’ютері (до 3 рядків)."}, {"text", "string", "Великий напис-результат."}, {"data.hit", "number", "Абсолютна секунда появи напису (типово at+1,3)."}, {"data.count", "number", "Лічильник угорі праворуч зупиняється на цьому числі."}, {"data.unit", "string", "Одиниця лічильника."}, {"data.app", "string", "Заголовок вікна (типово «AI助手» — краще задай свій)."}, {"dur", "number", "Скільки триває."}}, Doc: "Екран комп’ютера: набір тексту → відповідь сиплеться стіною коду → великий напис.", Example: json.RawMessage(`{"at":2,"kind":"insert","sub":"виправ одну літеру","text":"Змінено 40 файлів","data":{"hit":3.4,"app":"ШІ-помічник"}}`)},
		},
		Example: json.RawMessage(`{"grammar":"y4_storytime","duration":6,"cues":[{"at":0.1,"kind":"point","text":"Я попросив виправити одну літеру,","sub":"одну літеру"},{"at":2,"kind":"insert","sub":"виправ одну літеру","text":"Змінено 40 файлів","data":{"hit":3.4,"app":"ШІ-помічник"}},{"at":4.4,"kind":"react"}]}`),
	},
	{
		ID:          "y5_kinetic_type",
		Label:       "Кінетичний текст",
		Description: "Одна думка — один екран: велике слово влітає, кольорові сторінки змінюються шторкою, маркер виділяє головне, лічильник прогресу для списку.",
		Uses:        []string{"списки, етапи, кілька пунктів", "гасло, головна фраза", "одне велике число"},
		Avoid:       "коли є що показати на графіку чи схемі",
		Data:        []Field{{"palette", "array", "[[тло, колір тексту], …] — по черзі для сторінок."}},
		Cues: []CueKind{
			{Kind: "title", Aliases: []string{"point"}, Fields: []Field{{"text", "string", "Головне слово (довге — зменшується, до двох рядків)."}, {"sub", "string", "Допоміжна фраза."}, {"data.label", "string", "Мітка вгорі ліворуч."}, {"data.key", "string", "Слово в sub, яке підсвітити."}, {"data.num", "string|integer", "Великий номер на тлі."}}, Doc: "Сторінка: слово влітає в кадрі at (шторка — на 0,3 с раніше). Два і більше point — список із доріжкою прогресу.", Example: json.RawMessage(`{"at":1.8,"kind":"point","text":"Повільно","sub":"щоразу одна зміна","data":{"label":"01","key":"одна"}}`)},
			numCue,
			{Kind: "highlight", Fields: []Field{{"data.word", "string", "Слово з головного тексту (без нього — увесь)."}}, Doc: "Маркер і легке збільшення.", Example: json.RawMessage(`{"at":3,"kind":"highlight","data":{"word":"Повільно"}}`)},
		},
		Example: json.RawMessage(`{"grammar":"y5_kinetic_type","duration":5,"cues":[{"at":0,"kind":"title","text":"Повільно — це швидко"},{"at":1.8,"kind":"point","text":"Повільно","sub":"щоразу одна зміна","data":{"key":"одна"}},{"at":3.3,"kind":"point","text":"Швидко","sub":"працює з першого разу","data":{"key":"з першого"}}]}`),
	},
}
