package studio

// The creative catalogue the UI offers on step 1 and the planner turns into
// directions for the model: style categories, genres, cameras, tempos, voice
// tones and audience languages. Ids are stable (they are stored in projects
// and profiles); labels are Ukrainian; hints/directions are English (they go
// into the model prompts).

import "strings"

// Choice is one option of a creative select: a stable id, the Ukrainian
// label shown in the UI and the English direction sent to the model.
type Choice struct {
	ID    string
	Label string
	Hint  string
}

// Category is a group of the style gallery.
type Category struct {
	ID    string
	Label string
}

// StyleCategories are the style gallery groups, in display order.
var StyleCategories = []Category{
	{"animation", "Анімація"},
	{"comics", "Комікси"},
	{"cinema", "Кіно й фото"},
	{"art", "Мистецтво"},
	{"explainer", "Пояснювальні"},
	{"effects", "Ефекти"},
}

// Genres: the first twelve are the web studio's genre ids (step 1), the rest
// are popular YouTube niches. An id missing from the catalogue (a project that
// stored free text) is sent to the model as is.
var Genres = []Choice{
	{"fairy tale", "Казка", "Fairy tale: magical, warm and wondrous; clear good and evil, gentle humour, soft enchanted lighting and a kind moral at the end."},
	{"adventure", "Пригоди", "Adventure: a journey with rising stakes, discovery and courage; sweeping landscapes, momentum and hopeful energy."},
	{"drama", "Драма", "Drama: character-driven emotion, conflict and change; intimate framing, naturalistic light and meaningful pauses."},
	{"comedy", "Комедія", "Comedy: playful timing, exaggerated reactions and ironic contrast; bright lighting, lively pacing, a small punchline in every beat."},
	{"horror", "Хорор", "Horror: slowly building dread; darkness, negative space, unsettling details, cold desaturated light and sudden reveals."},
	{"thriller", "Трилер", "Thriller: tension and suspense, a ticking clock and twists; tight framing, hard contrast and a restless camera."},
	{"detective", "Детектив", "Detective story: clues, suspects and deduction; moody low-key lighting, close-ups on evidence and a satisfying reveal."},
	{"action", "Бойовик", "Action: kinetic set pieces and physical stakes; dynamic angles, motion, debris and relentless energy."},
	{"romance", "Романтика", "Romance: longing, chemistry and tender moments; soft warm light, close two-shots and a dreamy palette."},
	{"sci-fi", "Фантастика", "Science fiction: speculative technology and big ideas; clean futuristic design, cool palettes and a sense of wonder."},
	{"epic", "Епік", "Epic: grand scale, destiny and sacrifice; vast landscapes, armies and monuments, heroic low angles and golden light."},
	{"documentary", "Документальне", "Documentary: factual, clear and grounded; authentic locations, natural light, observational framing and a calm authoritative voice."},

	{"true crime", "Трукрайм", "True crime: a real-feeling case told step by step — timeline, evidence, suspects, motive; restrained and respectful, dim interiors, rain and cold blue light; no gore."},
	{"history", "Історична драма", "History drama: a vivid retelling of real events and people; period-accurate costumes, architecture and props; the stakes of the era explained clearly."},
	{"mythology", "Міфологія", "Mythology: gods, heroes and monsters of ancient myths; timeless epic imagery, symbolic colours, awe and fate."},
	{"parables", "Притчі й релігія", "Religious stories and parables: reverent, hopeful and simple; each story carries a clear moral or spiritual lesson; soft golden light."},
	{"psychology", "Психологія", "Psychology: how the mind works, shown through relatable everyday situations; introspective and calm, visual metaphors for inner states."},
	{"money", "Гроші й фінанси", "Money and finance stories: rise-and-fall journeys, business lessons and clear explanations of how money works; confident pacing, clean visuals of wealth, risk and consequence."},
	{"pov", "POV / інше життя", "POV / alternative life: told in the second person — what your day would be like in another era, place or situation; immersive first-person moments and vivid everyday details."},
	{"survival", "Виживання", "Survival: a person against nature or disaster; harsh environments, resourcefulness, rising danger and raw natural light."},
	{"space", "Космос", "Space: the scale and mystery of the universe — planets, stars, missions and astronauts; awe, silence and accurate science."},
	{"science", "Наука", "Science: curious, precise and accessible; explains one phenomenon step by step with vivid visual metaphors."},
	{"geopolitics", "Геополітика", "Geopolitics: countries, borders, resources and power explained clearly; maps, leaders and state symbols; serious analytical tone."},
	{"empire", "Королі й імперії", "Royalty and empires: kings, queens, dynasties and court intrigue, the rise and fall of empires; opulent palaces, armies and ceremony."},
	{"medieval fantasy", "Середньовічне фентезі", "Medieval fantasy: knights, castles, dragons and magic; mist, torchlight, heraldic colours, quests and loyalty."},
	{"mystery", "Загадки й таємниці", "Mystery: unexplained phenomena and unanswered questions; slow reveals, fog, shadows and curious details that keep the viewer guessing."},
	{"folk horror", "Фольклорний хорор", "Folk horror: ancient rural legends, rituals and creatures of folklore; eerie villages and forests, dread even in daylight."},
	{"animals", "Тварини", "Animals: fascinating animal lives and facts; close observation of behaviour, habitats and emotions."},
	{"bedtime", "Казка на ніч", "Kids bedtime story: soothing, safe and gentle; soft pastel night light, cosy places, no scary moments and a calm happy ending."},
	{"motivation", "Мотивація", "Motivational: struggle, discipline and breakthrough; an uplifting build-up, sunrise light and a powerful closing message."},
	{"book summary", "Переказ книги", "Book summary: the key ideas of a book told as a clear story, one idea per scene, each with a practical takeaway."},
	{"biography", "Біографія", "Biography: the life of a real person from origins to legacy; key turning points, period detail and human emotion."},
	{"explainer", "Пояснювальне відео", "Explainer: one clear idea per scene, from the question to the answer; concrete examples, numbers and comparisons that are easy to see."},
	{"top list", "Топ-список", "Top list: a countdown of items, one entry per scene with its number, a striking fact and a quick payoff; energetic and easy to follow."},
	{"product", "Огляд продукту", "Product story: the product, its details and the problem it solves, benefit by benefit; clean close-ups and a confident closing call to action."},
	{"travel", "Подорожі й місця", "Travel and places: atmospheric views of landscapes, streets and landmarks, a sense of discovery, local details and golden light."},
	{"history map", "Історія на мапі", "History told on a map: a short witty explainer of one real historical event — a one-sentence paradox hook, a quick rewind to the start, then a causal chain of beats with dates, numbers and prices, dry ironic asides and a punchline with a striking number; countries, borders, routes and armies shown on the map."},
}

// Cameras: the camera language of the shot prompts (the first four are the
// web studio's).
var Cameras = []Choice{
	{"cinematic", "Кінематографічна", "Camera language: cinematic — dramatic angles, depth of field, motivated camera moves."},
	{"medium", "Ближче до героїв", "Camera language: mostly medium shots and close-ups focused on characters."},
	{"wide", "Широкі плани", "Camera language: wide establishing shots emphasizing environment and scale."},
	{"dynamic", "Динамічна", "Camera language: dynamic — whip pans, fast push-ins, handheld energy."},
	{"handheld", "Ручна документальна", "Camera language: handheld documentary — slight natural shake, observational framing, follow-the-subject moves, available light."},
	{"drone", "Аерозйомка з дрона", "Camera language: aerial drone — high overhead views and sweeping flyovers, slow reveals of landscapes and scale."},
	{"tripod", "Статична камера", "Camera language: static tripod — locked-off, carefully composed frames; movement comes only from within the shot."},
	{"pov", "Від першої особи", "Camera language: first-person POV — the camera is the viewer's eyes, hands and body at the frame edges, immersive perspective."},
	{"macro", "Макро-деталі", "Camera language: macro detail — extreme close-ups of textures, objects and small actions, very shallow depth of field."},
	{"map-pan", "Рух мапою", "Camera language: an animated map — slow push-ins and pans across the map, zooms from one continent to another, elements popping in with a little bounce, lines drawing on, ships travelling along routes, colour fills spreading; no talking heads."},
}

// Tempos: the pacing of the film.
var Tempos = []Choice{
	{"slow", "Повільний", "Pacing: slow and contemplative, lingering shots."},
	{"balanced", "Рівний", "Pacing: balanced."},
	{"fast", "Швидкий", "Pacing: fast and energetic, punchy shots."},
	{"rapid", "Дуже швидкий (Shorts)", "Pacing: very fast Shorts rhythm — a new beat and a new picture every 2–3 seconds, one idea per line, no pauses, constant movement."},
}

// Tones: how the voice-over is written (and, slightly, spoken). "" = a
// neutral storyteller.
var Tones = []Choice{
	{"storyteller", "Теплий оповідач", "Voice-over tone: a warm, intimate storyteller — friendly and unhurried, vivid images, speaks directly to the listener."},
	{"documentary", "Документальний диктор", "Voice-over tone: a documentary narrator — calm, authoritative and precise; clear facts, measured rhythm, no slang."},
	{"trailer", "Драматичний трейлер", "Voice-over tone: a dramatic movie-trailer narrator — short punchy sentences, suspense, big stakes and powerful pauses."},
	{"youtuber", "Енергійний ютубер", "Voice-over tone: an excited YouTuber — energetic, casual and curious; hooks the viewer, asks questions, talks to \"you\"."},
	{"calm", "Спокійний, медитативний", "Voice-over tone: calm and meditative — soft, slow, soothing sentences with gentle imagery and room to breathe."},
	{"humor", "З гумором", "Voice-over tone: humorous — witty, ironic and playful; light jokes and comic timing without losing the story."},
	{"eerie", "Моторошний", "Voice-over tone: creepy and eerie — hushed, ominous, slow-burning suspense; unsettling details and quiet dread."},
	{"kids", "Лагідний для дітей", "Voice-over tone: gentle, for young children — simple words, short sentences, warm and reassuring, full of wonder."},
	{"motivational", "Мотиваційний", "Voice-over tone: motivational — confident, inspiring and direct; builds energy toward a powerful call to action."},
	{"witty-explainer", "Дотепний швидкий пояснювач", "Voice-over tone: a witty, fast history explainer — casual and conversational, energetic and punchy, short sentences full of dates and numbers, dry ironic one-line asides, light humour and a punchline at the end; talks to \"you\", never lectures."},
}

// Language is an audience language of the film (narration, titles,
// subtitles, voice).
type Language struct {
	Code    string // ISO 639-1 where it exists ("uk", "en"), else ISO 639-3 ("fil", "yue", "ceb")
	English string // English name (used in the model prompts)
	Native  string // self-name
	Label   string // Ukrainian name shown in the UI
}

// Languages: every language ElevenLabs Eleven v3 speaks plus the extra ones
// of the newer multilingual models. Ukrainian and English first, the rest in
// English alphabetical order.
var Languages = []Language{
	{"uk", "Ukrainian", "Українська", "Українська"},
	{"en", "English", "English", "Англійська"},
	{"af", "Afrikaans", "Afrikaans", "Африкаанс"},
	{"am", "Amharic", "አማርኛ", "Амхарська"},
	{"ar", "Arabic", "العربية", "Арабська"},
	{"hy", "Armenian", "Հայերեն", "Вірменська"},
	{"as", "Assamese", "অসমীয়া", "Ассамська"},
	{"ast", "Asturian", "Asturianu", "Астурійська"},
	{"az", "Azerbaijani", "Azərbaycan dili", "Азербайджанська"},
	{"be", "Belarusian", "Беларуская", "Білоруська"},
	{"bn", "Bengali", "বাংলা", "Бенгальська"},
	{"bs", "Bosnian", "Bosanski", "Боснійська"},
	{"bg", "Bulgarian", "Български", "Болгарська"},
	{"my", "Burmese", "မြန်မာ", "Бірманська"},
	{"yue", "Cantonese", "粵語", "Кантонська"},
	{"ca", "Catalan", "Català", "Каталанська"},
	{"ceb", "Cebuano", "Cebuano", "Себуанська"},
	{"ny", "Chichewa", "Chichewa", "Чічева"},
	{"zh", "Chinese (Mandarin)", "中文（普通话）", "Китайська (мандаринська)"},
	{"hr", "Croatian", "Hrvatski", "Хорватська"},
	{"cs", "Czech", "Čeština", "Чеська"},
	{"da", "Danish", "Dansk", "Данська"},
	{"nl", "Dutch", "Nederlands", "Нідерландська"},
	{"et", "Estonian", "Eesti", "Естонська"},
	{"fil", "Filipino", "Filipino", "Філіппінська"},
	{"fi", "Finnish", "Suomi", "Фінська"},
	{"fr", "French", "Français", "Французька"},
	{"ff", "Fula", "Fulfulde", "Фула"},
	{"gl", "Galician", "Galego", "Галісійська"},
	{"ka", "Georgian", "ქართული", "Грузинська"},
	{"de", "German", "Deutsch", "Німецька"},
	{"el", "Greek", "Ελληνικά", "Грецька"},
	{"gu", "Gujarati", "ગુજરાતી", "Гуджараті"},
	{"ha", "Hausa", "Hausa", "Хауса"},
	{"he", "Hebrew", "עברית", "Іврит"},
	{"hi", "Hindi", "हिन्दी", "Гінді"},
	{"hu", "Hungarian", "Magyar", "Угорська"},
	{"is", "Icelandic", "Íslenska", "Ісландська"},
	{"id", "Indonesian", "Bahasa Indonesia", "Індонезійська"},
	{"ga", "Irish", "Gaeilge", "Ірландська"},
	{"it", "Italian", "Italiano", "Італійська"},
	{"ja", "Japanese", "日本語", "Японська"},
	{"jv", "Javanese", "Basa Jawa", "Яванська"},
	{"kam", "Kamba", "Kikamba", "Камба"},
	{"kn", "Kannada", "ಕನ್ನಡ", "Каннада"},
	{"kk", "Kazakh", "Қазақ тілі", "Казахська"},
	{"ko", "Korean", "한국어", "Корейська"},
	{"ckb", "Kurdish (Sorani)", "کوردیی ناوەندی", "Курдська (сорані)"},
	{"ky", "Kyrgyz", "Кыргызча", "Киргизька"},
	{"lo", "Lao", "ລາວ", "Лаоська"},
	{"lv", "Latvian", "Latviešu", "Латиська"},
	{"ln", "Lingala", "Lingála", "Лінгала"},
	{"lt", "Lithuanian", "Lietuvių", "Литовська"},
	{"lg", "Luganda", "Luganda", "Луганда"},
	{"lb", "Luxembourgish", "Lëtzebuergesch", "Люксембурзька"},
	{"mk", "Macedonian", "Македонски", "Македонська"},
	{"ms", "Malay", "Bahasa Melayu", "Малайська"},
	{"ml", "Malayalam", "മലയാളം", "Малаялам"},
	{"mt", "Maltese", "Malti", "Мальтійська"},
	{"mi", "Māori", "Te Reo Māori", "Маорі"},
	{"mr", "Marathi", "मराठी", "Маратхі"},
	{"mn", "Mongolian", "Монгол хэл", "Монгольська"},
	{"ne", "Nepali", "नेपाली", "Непальська"},
	{"no", "Norwegian", "Norsk", "Норвезька"},
	{"oc", "Occitan", "Occitan", "Окситанська"},
	{"or", "Odia", "ଓଡ଼ିଆ", "Орія"},
	{"ps", "Pashto", "پښتو", "Пушту"},
	{"fa", "Persian", "فارسی", "Перська"},
	{"pl", "Polish", "Polski", "Польська"},
	{"pt", "Portuguese", "Português", "Португальська"},
	{"pa", "Punjabi", "ਪੰਜਾਬੀ", "Панджабі"},
	{"ro", "Romanian", "Română", "Румунська"},
	{"ru", "Russian", "Русский", "Російська"},
	{"sr", "Serbian", "Српски", "Сербська"},
	{"sn", "Shona", "chiShona", "Шона"},
	{"sd", "Sindhi", "سنڌي", "Сіндхі"},
	{"sk", "Slovak", "Slovenčina", "Словацька"},
	{"sl", "Slovenian", "Slovenščina", "Словенська"},
	{"so", "Somali", "Soomaali", "Сомалійська"},
	{"es", "Spanish", "Español", "Іспанська"},
	{"sw", "Swahili", "Kiswahili", "Суахілі"},
	{"sv", "Swedish", "Svenska", "Шведська"},
	{"tg", "Tajik", "Тоҷикӣ", "Таджицька"},
	{"ta", "Tamil", "தமிழ்", "Тамільська"},
	{"te", "Telugu", "తెలుగు", "Телугу"},
	{"th", "Thai", "ไทย", "Тайська"},
	{"tr", "Turkish", "Türkçe", "Турецька"},
	{"ur", "Urdu", "اردو", "Урду"},
	{"uz", "Uzbek", "Oʻzbekcha", "Узбецька"},
	{"vi", "Vietnamese", "Tiếng Việt", "В'єтнамська"},
	{"cy", "Welsh", "Cymraeg", "Валлійська"},
	{"wo", "Wolof", "Wolof", "Волоф"},
	{"zu", "Zulu", "isiZulu", "Зулу"},
}

// ChoiceByID finds an option of a catalogue list (Genres, Cameras, Tempos, Tones).
func ChoiceByID(list []Choice, id string) (Choice, bool) {
	for _, c := range list {
		if c.ID == id {
			return c, true
		}
	}
	return Choice{}, false
}

// LanguageByCode finds an audience language (case-insensitive code).
func LanguageByCode(code string) (Language, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, l := range Languages {
		if l.Code == code {
			return l, true
		}
	}
	return Language{}, false
}

// hintOf is the direction of a catalogue option ("" when unknown).
func hintOf(list []Choice, id string) string {
	if c, ok := ChoiceByID(list, id); ok {
		return c.Hint
	}
	return ""
}

// knownCamera reports a camera id of the catalogue.
func knownCamera(id string) bool {
	_, ok := ChoiceByID(Cameras, id)
	return ok
}

// languageName is the English name of an audience language for the model
// prompts; an unknown code is passed through as is ("" = auto).
func languageName(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if l, ok := LanguageByCode(code); ok {
		return l.English
	}
	return code
}

// NarratorStyle is the kind of narrator voice a profile asks for
// (Input.NarratorStyle): while no narrator voice is chosen, the plan picks
// one of the current TTS provider's voices that fits it. The user's own
// choice (the project's or the profile's voice) always wins.
type NarratorStyle struct {
	ID     string
	Label  string // Ukrainian
	Gender string // "male" | "female"
	Age    string // AgeYoung | AgeAdult
	// Voices are fitting voices of the providers whose voices are fixed
	// (tts/catalog ids: OpenAI, Google Chirp 3 HD, Azure), taken when the
	// account lists them; the other providers pick by gender and age.
	Voices []string
}

// NarratorStyles are the narrator kinds of the profile editor ("" = any).
var NarratorStyles = []NarratorStyle{
	{ID: "male-energetic", Label: "Чоловічий, енергійний", Gender: "male", Age: AgeYoung,
		Voices: []string{"Chirp3-HD-Puck", "Chirp3-HD-Fenrir", "en-US-BrianMultilingualNeural", "en-US-AndrewMultilingualNeural", "ash", "echo"}},
	{ID: "male-calm", Label: "Чоловічий, спокійний", Gender: "male", Age: AgeAdult,
		Voices: []string{"Chirp3-HD-Charon", "en-US-AndrewMultilingualNeural", "onyx"}},
	{ID: "female-energetic", Label: "Жіночий, енергійний", Gender: "female", Age: AgeYoung,
		Voices: []string{"Chirp3-HD-Laomedeia", "Chirp3-HD-Aoede", "en-US-AvaMultilingualNeural", "coral", "nova"}},
	{ID: "female-calm", Label: "Жіночий, спокійний", Gender: "female", Age: AgeAdult,
		Voices: []string{"Chirp3-HD-Kore", "en-US-EmmaMultilingualNeural", "sage", "shimmer"}},
}

// NarratorStyleByID finds a narrator kind.
func NarratorStyleByID(id string) (NarratorStyle, bool) {
	for _, s := range NarratorStyles {
		if s.ID == id {
			return s, true
		}
	}
	return NarratorStyle{}, false
}

// NarratorStyleChoices are the narrator kinds as a select's options (the
// picker offers "" = any voice itself).
func NarratorStyleChoices() []Choice {
	var out []Choice
	for _, s := range NarratorStyles {
		out = append(out, Choice{ID: s.ID, Label: s.Label})
	}
	return out
}
