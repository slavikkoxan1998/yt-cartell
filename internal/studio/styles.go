package studio

// The built-in visual styles (id → label + prompt text). The first 23 are the
// port of services/styles.ts (same ids, labels and prompts, same order); the
// rest extend the catalogue with the looks faceless YouTube studios offer.
// Every style has a preview image (internal/assets/styles/<id>.jpg, rendered
// by cmd/stylegen); Palette paints a fallback tile when one is missing.

import "strings"

// StyleDef is one built-in visual style.
type StyleDef struct {
	ID     string
	Label  string // Ukrainian name shown in the gallery
	Prompt string // English style prompt sent to the image model
	// Category groups the gallery: one of the StyleCategories ids
	// ("animation", "comics", "cinema", "art", "explainer", "effects").
	Category string
	// Suits is a short Ukrainian hint "підходить для …".
	Suits string
	// Palette: three hex colours for a typographic tile when there is no preview image.
	Palette [3]string
	// Direction (optional, English) is what this style asks of the planner
	// beyond the look: how the cast is drawn, what every picture must state.
	// It joins the bible's and the shot writer's directions.
	Direction string
	// TextPolicy (optional, English) replaces the studio's "no text" line in
	// the frame prompts of a style whose pictures carry a little lettering
	// (map labels, price tags); "" = no text at all.
	TextPolicy string
}

// styleOf is the built-in style a project uses (false for a custom style
// or an unknown id).
func styleOf(in Input) (StyleDef, bool) {
	if strings.TrimSpace(in.CustomStyle) != "" {
		return StyleDef{}, false
	}
	return StyleByID(in.Style)
}

// frameTextRule is the lettering rule of a project's frame prompts: the
// style's TextPolicy, else no text at all.
func frameTextRule(in Input) string {
	if s, ok := styleOf(in); ok && s.TextPolicy != "" {
		return s.TextPolicy
	}
	return noText
}

// Styles are the built-in styles: the 23 web-studio styles first, then the
// extended catalogue.
var Styles = []StyleDef{
	// ---- the web studio's 23 (services/styles.ts) ----------------------------------------
	{ID: "cinematic", Label: "Кіно", Prompt: "Cinematic film still: anamorphic lens, shallow depth of field, dramatic lighting, rich color grading, 35mm film look.",
		Category: "cinema", Suits: "підходить для драм, трилерів і майже будь-якої історії", Palette: [3]string{"#0F1A24", "#D98E04", "#4A6B8A"}},
	{ID: "realism", Label: "Реалізм", Prompt: "Photorealistic: natural lighting, realistic textures and skin detail, true-to-life proportions, high detail.",
		Category: "cinema", Suits: "підходить для правдивих, побутових і документальних історій", Palette: [3]string{"#2F3E46", "#84A98C", "#CAD2C5"}},
	{ID: "2d", Label: "2D-анімація", Prompt: "Clean 2D animation style: crisp line art, flat cel shading, bright harmonious colors, animated series look.",
		Category: "animation", Suits: "підходить для мультсеріалів, казок і гумору", Palette: [3]string{"#FFB703", "#219EBC", "#FB8500"}},
	{ID: "npr-2point5d", Label: "2.5D мальований", Prompt: "Stylized 2.5D non-photorealistic render: painterly 3D, soft cel lighting, illustrated textures.",
		Category: "animation", Suits: "підходить для казок, пригод і м'яких драм", Palette: [3]string{"#6D597A", "#E56B6F", "#EAAC8B"}},
	{ID: "comics", Label: "Комікс", Prompt: "Classic comic book art: bold ink outlines, flat vivid colors, halftone shading, dynamic composition.",
		Category: "comics", Suits: "підходить для пригод, екшену й героїчних історій", Palette: [3]string{"#FFD60A", "#D62828", "#003049"}},
	{ID: "retro-comics", Label: "Ретро-комікс", Prompt: "1950s–60s retro comic style: Ben-Day dots, limited CMYK palette, aged paper look.",
		Category: "comics", Suits: "підходить для ретро-історій, пародій і епохи 50–60-х", Palette: [3]string{"#F1E4C3", "#E63946", "#1D3557"}},
	{ID: "dirty-comics", Label: "Брудний комікс", Prompt: "Gritty underground comic art: rough heavy inks, grimy textures, muted dirty palette, crosshatching.",
		Category: "comics", Suits: "підходить для кримінальних, темних і панк-історій", Palette: [3]string{"#2B2B2B", "#6B4E3D", "#A8977B"}},
	{ID: "manga", Label: "Манґа", Prompt: "Japanese manga style: expressive eyes, screentone shading, clean black ink lines, dynamic speed lines.",
		Category: "comics", Suits: "підходить для аніме-історій, драм і пригод", Palette: [3]string{"#FFFFFF", "#111111", "#9E9E9E"}},
	{ID: "noir", Label: "Нуар", Prompt: "Film noir: high-contrast black and white, deep shadows, venetian-blind light, smoky moody atmosphere.",
		Category: "cinema", Suits: "підходить для детективів, трукраймів і загадок", Palette: [3]string{"#0A0A0A", "#5C5C5C", "#E6E6E6"}},
	{ID: "watercolor", Label: "Акварель", Prompt: "Watercolor painting: soft washes, bleeding pigments, visible paper grain, gentle pastel palette.",
		Category: "art", Suits: "підходить для казок, ліричних і спокійних історій", Palette: [3]string{"#A8DADC", "#F1FAEE", "#E5989B"}},
	{ID: "oil-painting", Label: "Олійний живопис", Prompt: "Classical oil painting: rich impasto brushstrokes, chiaroscuro lighting, canvas texture, old-master palette.",
		Category: "art", Suits: "підходить для історії, епосу й біографій", Palette: [3]string{"#3D2B1F", "#A47148", "#E0C9A6"}},
	{ID: "sketch", Label: "Скетч", Prompt: "Pencil sketch: graphite lines, loose hatching, visible construction lines, paper texture, monochrome.",
		Category: "art", Suits: "підходить для спогадів, щоденникових історій і пояснень", Palette: [3]string{"#F5F5F0", "#4A4A4A", "#9A9A9A"}},
	{ID: "claymation", Label: "Пластилін", Prompt: "Claymation stop-motion: sculpted plasticine characters, fingerprint textures, miniature handmade sets.",
		Category: "animation", Suits: "підходить для гумору, дитячих і сімейних історій", Palette: [3]string{"#E76F51", "#F4A261", "#2A9D8F"}},
	{ID: "pixel-art", Label: "Піксель-арт", Prompt: "16-bit pixel art: crisp visible pixels, limited palette, retro video game aesthetic.",
		Category: "effects", Suits: "підходить для ігрових тем, ретро й гумору", Palette: [3]string{"#1B1B3A", "#F72585", "#4CC9F0"}},
	{ID: "classic-cartoon", Label: "Класичний мультик", Prompt: "Classic Saturday-morning cartoon: thick outlines, bright primary colors, squash-and-stretch expressions.",
		Category: "animation", Suits: "підходить для гумору й дитячих пригод", Palette: [3]string{"#FFBE0B", "#FB5607", "#3A86FF"}},
	{ID: "rubber-hose-modern", Label: "Rubber Hose", Prompt: "Modern rubber hose animation: bendy noodle limbs, pie-cut eyes, 1930s cartoon charm with modern colors.",
		Category: "animation", Suits: "підходить для гумору, ретро-казок і музичних історій", Palette: [3]string{"#F2E8CF", "#BC4749", "#386641"}},
	{ID: "rotoscope", Label: "Ротоскоп", Prompt: "Rotoscope animation: traced realistic motion, flat color fills with outlined shapes, dreamlike look.",
		Category: "animation", Suits: "підходить для снів, психології й незвичних історій", Palette: [3]string{"#264653", "#E9C46A", "#F4A261"}},
	{ID: "vector-minimalism", Label: "Векторний мінімалізм", Prompt: "Flat vector minimalism: clean geometric shapes, smooth gradients, limited modern palette, crisp edges.",
		Category: "explainer", Suits: "підходить для пояснень, бізнесу й технологій", Palette: [3]string{"#F8F9FA", "#4361EE", "#F72585"}},
	{ID: "quirky-minimalist", Label: "Мінімалізм", Prompt: "Quirky minimalist illustration: simple geometric shapes, lots of negative space, few flat colors, playful.",
		Category: "explainer", Suits: "підходить для гумору, психології й коротких пояснень", Palette: [3]string{"#FEFAE0", "#DDA15E", "#283618"}},
	{ID: "mixed-media-collage", Label: "Колаж", Prompt: "Mixed media collage: cut-out paper, magazine clippings, torn edges, layered textures and paint.",
		Category: "art", Suits: "підходить для історії, культури й відео-есеїв", Palette: [3]string{"#EDE0D4", "#9C6644", "#7F5539"}},
	{ID: "underground", Label: "Андеграунд", Prompt: "Underground zine art: raw punk energy, photocopy texture, xerox grain, bold stark contrasts.",
		Category: "art", Suits: "підходить для панку, сатири й контркультури", Palette: [3]string{"#F0F0F0", "#111111", "#FF006E"}},
	{ID: "gross-out", Label: "Гротеск", Prompt: "Gross-out cartoon style: exaggerated grotesque close-ups, detailed textures, saturated colors, comedic.",
		Category: "animation", Suits: "підходить для абсурдного гумору й пародій", Palette: [3]string{"#9ACD32", "#FF69B4", "#8B4513"}},
	{ID: "stickman", Label: "Стікмен", Prompt: "Stick-figure drawing: simple black line stickmen, minimal doodle backgrounds, white paper, humorous.",
		Category: "explainer", Suits: "підходить для гумору, лайфхаків і простих пояснень", Palette: [3]string{"#FFFFFF", "#000000", "#E5E5E5"}},

	// ---- explainer -------------------------------------------------------------------------
	{ID: "science-explainer", Label: "Науковий пояснювач",
		Prompt:   "Flat-shape science explainer illustration: simple rounded geometric shapes with no outlines, soft inner glow and luminous gradients, bold saturated colours on a deep navy background, cute minimal characters with dot eyes, clean vector look, gentle depth from layered shapes and long soft shadows.",
		Category: "explainer", Suits: "підходить для науки, космосу й пояснень «як це працює»", Palette: [3]string{"#1B1F3B", "#FF6F59", "#43D9AD"}},
	{ID: "chalkboard", Label: "Крейдяна дошка",
		Prompt:   "Chalkboard explainer: hand-drawn chalk illustrations and diagrams on a dark green slate board, dusty chalk texture and smudges, white chalk lines with a few coloured-chalk accents, arrows and symbols instead of words, soft classroom light.",
		Category: "explainer", Suits: "підходить для уроків, історії науки й освітніх пояснень", Palette: [3]string{"#1F3A2E", "#F2F2E9", "#F5D76E"}},
	{ID: "whiteboard", Label: "Дошка з маркером",
		Prompt:   "Whiteboard doodle animation style: black marker line drawings on a clean white board, simple expressive doodle characters and icons, a few red and blue marker accents, hand-drawn arrows, minimal shading, bright even lighting.",
		Category: "explainer", Suits: "підходить для бізнес-пояснень, лайфхаків і мотивації", Palette: [3]string{"#FFFFFF", "#1E1E1E", "#E63946"}},
	{ID: "infographic", Label: "Інфографіка",
		Prompt:   "Documentary infographic style: clean data-driven compositions, bold flat icons, maps, charts and diagram shapes, strong grid layout, limited palette of deep navy, cyan and signal yellow, crisp vector edges, modern news-graphics look with subtle depth and shadows.",
		Category: "explainer", Suits: "підходить для фінансів, геополітики, порівнянь і статистики", Palette: [3]string{"#0F172A", "#38BDF8", "#FACC15"}},
	{ID: "isometric", Label: "Ізометрична діорама",
		Prompt:   "Isometric diorama: a miniature 3D scene in true isometric projection on a floating cut-away base, clean stylised models, soft pastel colours, gentle global illumination and contact shadows, tidy tiny details, toy-like tilt-shift feel.",
		Category: "explainer", Suits: "підходить для пояснень, бізнесу, міст і процесів", Palette: [3]string{"#E9EEF2", "#5C7CFA", "#FF922B"}},
	{ID: "vintage-map", Label: "Стара мапа",
		Prompt:   "Vintage cartography: antique hand-drawn map illustration on aged parchment, inked coastlines, rivers and mountains, sepia and faded blue washes, compass roses, tiny ships and sea monsters as decorative drawings, no readable lettering, age-of-exploration atmosphere.",
		Category: "explainer", Suits: "підходить для історії, геополітики, подорожей і воєн", Palette: [3]string{"#E8D8B0", "#8B5E34", "#2F4858"}},
	{ID: "history-map-pegs", Label: "Мапа-історія (фігурки)",
		Prompt: "Animated history-explainer map illustration: an antique vintage world map on aged parchment — beige-tan land with subtle paper texture, " +
			"muted teal-blue ocean, thin inked coastlines; countries shown as FLAT solid colour regions (royal blue, red, teal, orange, green) with " +
			"crisp borders and a soft glow on highlighted outlines; a few short labels in old-atlas serif type; small clean icons (tiny sailing ships, " +
			"stacks of cash, cargo crates, price tags, arrows, dotted routes). Characters are cute chibi peg-doll figures: rounded capsule bodies with " +
			"tiny or no arms, big round heads, simple dot eyes and a tiny mouth, period costumes, soft shading and a slight dark outline, standing on " +
			"the map like game pawns. Clean 2D vector look, soft even light, gentle vignette. Avoid: realistic people, 3D render look, glossy CGI, " +
			"paragraphs of text, modern UI.",
		Category: "explainer", Suits: "підходить для історії на мапі, геополітики й коротких пояснень у Shorts", Palette: [3]string{"#D9C7A0", "#3E7C8C", "#2848A8"},
		Direction: "Map-history style: every picture is a map scene. Characters are chibi peg-doll figures (rounded capsule body, big round head, " +
			"dot eyes, tiny mouth, a period costume and hat that identify them) — describe each character by that costume, hat and colours, never as " +
			"a realistic person; a real historical figure appears only as such a figure (at most a rare painted-portrait cameo). Every image prompt " +
			"states the map state: the region in view, which countries or territories are filled in which flat colour (keep each country's colour " +
			"for the whole film), the few short labels or year/price tags on it, the icons, and which figures stand where on the map and what they do. " +
			"Leave the lower third of the frame calm for subtitles.",
		TextPolicy: "No captions, subtitles, speech bubbles, watermark or paragraphs of text; the only lettering allowed is a few short map labels " +
			"(country or region names), a year or a price tag, in clean old-atlas serif type, spelled exactly as the prompt gives them.",
	},

	// ---- motion graphics and films without people (Input.Content) ---------------------------
	{ID: "motion-graphics", Label: "Моушн-графіка",
		Prompt:   "Modern motion-graphics design frame: bold flat and soft-3D shapes, clean icons and simple geometric forms, smooth gradients on a deep coloured backdrop, crisp vector edges, generous negative space, subtle depth with soft shadows and a gentle glow, polished broadcast-design look.",
		Category: "explainer", Suits: "підходить для пояснень, фактів, бізнесу й технологій", Palette: [3]string{"#111827", "#7C3AED", "#22D3EE"}},
	{ID: "kinetic-type", Label: "Кінетична типографіка",
		Prompt:   "Kinetic typography backdrop: a bold graphic poster composition made for big animated words — strong colour blocks, clean geometric shapes, lines, arrows and frames, high contrast, wide clean empty areas where the words will go, flat modern editorial look.",
		Category: "explainer", Suits: "підходить для цитат, мотивації, списків і коротких фактів", Palette: [3]string{"#0B0B0B", "#FF3D57", "#FFE45C"}},
	{ID: "documentary-broll", Label: "Документальний b-roll",
		Prompt:   "Documentary b-roll cinematography: real places, objects and details without people, natural available light, gentle slider and handheld moves, shallow depth of field, true-to-life colour grading, textured close-ups, the calm premium look of a modern documentary.",
		Category: "cinema", Suits: "підходить для фактів, історії місць, природи й відео без людей", Palette: [3]string{"#1E2A2F", "#7D8F69", "#D8C3A5"}},
	{ID: "product-shot", Label: "Предметна зйомка",
		Prompt:   "Premium product and still-life photography: a hero object on a clean surface, soft sculpted studio lighting with controlled reflections, seamless backdrop, crisp macro detail, shallow depth of field, elegant minimal composition, high-end commercial look.",
		Category: "cinema", Suits: "підходить для оглядів, реклами, брендів і предметів", Palette: [3]string{"#F4F1EC", "#C08552", "#2E2E2E"}},

	// ---- cinema -------------------------------------------------------------------------------
	{ID: "archival-footage", Label: "Архівна плівка",
		Prompt:   "Degraded archival film footage: grainy black-and-white or faded sepia 16mm film, scratches, dust, flicker, light leaks and heavy vignetting, soft focus, period-accurate people, clothes and places, as if found in an old historical archive.",
		Category: "cinema", Suits: "підходить для історії, трукраймів і загадок минулого", Palette: [3]string{"#2B2622", "#A89F91", "#E8E0D0"}},
	{ID: "photo-1940s", Label: "Фото 1940-х",
		Prompt:   "1940s photorealism: authentic 1940s press photograph, silver-gelatin black-and-white with fine grain, period clothing, hairstyles, vehicles and architecture, natural available light, candid documentary framing, slight lens softness.",
		Category: "cinema", Suits: "підходить для історій Другої світової, біографій і епохи", Palette: [3]string{"#3A3A3A", "#BDB5A6", "#F0EBE0"}},
	{ID: "aged-realism", Label: "Історичний реалізм",
		Prompt:   "Aged historical realism: photorealistic recreation of the past with a weathered, time-worn look, muted desaturated earth tones, soft diffused light, authentic costumes, tools and architecture of the era, subtle grain and faded colours like a restored antique colour photograph.",
		Category: "cinema", Suits: "підходить для історичних драм, імперій і давніх цивілізацій", Palette: [3]string{"#4A3B2A", "#9C8566", "#D9C9A8"}},
	{ID: "faceless-cinematic", Label: "Кіно без облич",
		Prompt:   "Faceless cinematic realism: photorealistic film still in which faces are never visible — people seen from behind, in silhouette, cropped at the shoulders, turned away or lost in shadow; focus on hands, gestures, objects and places; moody motivated lighting, shallow depth of field, anamorphic look.",
		Category: "cinema", Suits: "підходить для фейслес-каналів, трукраймів і психології", Palette: [3]string{"#101418", "#3E5C76", "#E0B872"}},
	{ID: "wildlife-doc", Label: "Дика природа",
		Prompt:   "Wildlife documentary cinematography: ultra-sharp telephoto nature footage, animals in their natural habitat, golden-hour backlight, shallow depth of field, crisp fur and feather detail, natural colour grading, patient observational framing of a premium nature series.",
		Category: "cinema", Suits: "підходить для тварин, природи й фактів про планету", Palette: [3]string{"#23361E", "#8BA35B", "#E3C58F"}},
	{ID: "botanical-macro", Label: "Макро-ботаніка",
		Prompt:   "Botanical macro cinematography: extreme close-ups of plants, leaves, dew drops, seeds and flowers, razor-thin depth of field with creamy bokeh, translucent backlit petals, intricate natural textures, soft natural light, calm scientific wonder.",
		Category: "cinema", Suits: "підходить для природи, науки й медитативних відео", Palette: [3]string{"#0E2A1F", "#6FBF73", "#F4E285"}},
	{ID: "space-drama", Label: "Космічна драма",
		Prompt:   "Space drama: photorealistic science-fiction film still, vast starfields and planets, realistic spacecraft and astronaut suits, hard directional sunlight with deep black shadows, subtle lens flares, cold blue against warm orange, epic scale and isolation.",
		Category: "cinema", Suits: "підходить для космосу, наукової фантастики й історій астронавтів", Palette: [3]string{"#05070D", "#2E4A7D", "#F28C28"}},
	{ID: "soviet-realism", Label: "Радянський ретрореалізм",
		Prompt:   "Soviet-era retro realism: 1970s–80s Eastern European colour photograph, faded warm film stock, prefabricated apartment blocks, period interiors, clothes and cars, soft muted reds and browns, slight grain and colour cast, everyday nostalgic atmosphere.",
		Category: "cinema", Suits: "підходить для історій про СРСР, ностальгії й холодної війни", Palette: [3]string{"#5A1E1E", "#C9A66B", "#E8E1D0"}},

	// ---- animation ----------------------------------------------------------------------------
	{ID: "family-3d", Label: "Сімейна 3D-анімація",
		Prompt:   "Family 3D animated feature style: appealing stylised characters with big expressive eyes, soft subsurface-scattered skin, rich global illumination, warm cinematic lighting, detailed yet friendly environments, vibrant colours, high-end studio CG render.",
		Category: "animation", Suits: "підходить для дитячих і сімейних історій, казок", Palette: [3]string{"#FFD166", "#06D6A0", "#118AB2"}},
	{ID: "soviet-animation", Label: "Класична радянська анімація",
		Prompt:   "Classic Soviet-era hand-drawn animation: 1950s–80s studio cartoon look, soft painted gouache backgrounds, gentle rounded character designs, warm muted palette, visible hand-inked lines, fairy-tale charm and kind expressions.",
		Category: "animation", Suits: "підходить для казок, дитячих історій і ностальгії", Palette: [3]string{"#F3E9D2", "#D1495B", "#00798C"}},
	{ID: "papercraft", Label: "Паперова аплікація",
		Prompt:   "Papercraft cut-out: layered paper and cardboard shapes with visible cut edges, soft drop shadows between layers, paper grain and fibres, handmade diorama depth, bright craft-paper colours, stop-motion charm.",
		Category: "animation", Suits: "підходить для казок, дитячих і освітніх відео", Palette: [3]string{"#F4D35E", "#EE964B", "#0D3B66"}},
	{ID: "felt-stop-motion", Label: "Фетрова анімація",
		Prompt:   "Felt stop-motion: handmade characters and sets sewn from felt and wool, fuzzy fibre textures, visible stitches and buttons, miniature tabletop scale, soft warm practical lighting, shallow depth of field, cosy handcrafted feel.",
		Category: "animation", Suits: "підходить для затишних дитячих історій і казок на ніч", Palette: [3]string{"#C1666B", "#D4B483", "#48A9A6"}},
	{ID: "anime-glow", Label: "Сяюче аніме",
		Prompt:   "Glowing anime cel style: luminous hand-painted anime backgrounds with dramatic skies, sparkling light, lens flares and floating glowing particles, clean cel-shaded characters, vivid sunset and twilight colours, emotional lyrical atmosphere.",
		Category: "animation", Suits: "підходить для романтики, мрій і ліричних історій", Palette: [3]string{"#1D3557", "#F4A6C1", "#FFD6A5"}},
	{ID: "anime-drama", Label: "Драматичне аніме",
		Prompt:   "Dramatic anime realism: detailed mature anime style with realistic proportions, sharp cel shading, intense lighting and deep shadows, gritty textures, cinematic camera angles, muted palette with striking accent colours, high-stakes emotion.",
		Category: "animation", Suits: "підходить для екшену, трилерів і драм", Palette: [3]string{"#1A1A2E", "#E94560", "#A8A8A8"}},
	{ID: "chibi", Label: "Чібі",
		Prompt:   "Chibi style: super-deformed cute characters with oversized heads, tiny bodies and big sparkling eyes, simple rounded shapes, pastel colours, soft cel shading, playful exaggerated expressions and comedic poses.",
		Category: "animation", Suits: "підходить для гумору, коротких смішних історій і дітей", Palette: [3]string{"#FFC8DD", "#BDE0FE", "#FFAFCC"}},
	{ID: "marker-cartoon", Label: "Маркерний мультик",
		Prompt:   "Marker cartoon: bold hand-drawn felt-tip marker illustration, thick wobbly black outlines, streaky marker colour fills with visible strokes, simple expressive characters, white paper background, energetic sketchbook humour.",
		Category: "animation", Suits: "підходить для гумору, пояснень і коротких скетчів", Palette: [3]string{"#FFFFFF", "#FF595E", "#1982C4"}},

	// ---- comics ---------------------------------------------------------------------------------
	{ID: "grunge-novel", Label: "Гранж-графічний роман",
		Prompt:   "Grunge graphic novel: gritty mature comic art, heavy ink blacks, rough splattered textures, distressed paper, desaturated palette with blood-red accents, dramatic chiaroscuro, angular expressive characters, noir panel-like compositions.",
		Category: "comics", Suits: "підходить для трилерів, кримінальних і темних історій", Palette: [3]string{"#141414", "#8C1C13", "#C9B79C"}},
	{ID: "fantasy-comic", Label: "Фентезі-комікс",
		Prompt:   "Fantasy adventure comic: vibrant heroic comic-book illustration, clean confident inks, rich saturated colours, dynamic poses and sweeping landscapes, magical light effects, detailed armour and costumes, epic storytelling energy.",
		Category: "comics", Suits: "підходить для пригод, міфології й фентезі-світів", Palette: [3]string{"#1C3F60", "#F2A541", "#E94F37"}},

	// ---- art -------------------------------------------------------------------------------------
	{ID: "golden-allegory", Label: "Золота алегорія",
		Prompt:   "Golden-light allegory: timeless symbolic painting with warm golden god-rays, glowing haze and soft bloom, simple archetypal figures in classical robes, rich amber and cream palette, serene reverent mood, luminous painterly realism of a parable illustration.",
		Category: "art", Suits: "підходить для притч, релігії, філософії й мотивації", Palette: [3]string{"#2E1F0F", "#C8962E", "#F6E3B4"}},
	{ID: "ink-manuscript", Label: "Середньовічний рукопис",
		Prompt:   "Medieval illuminated manuscript: flat ink and tempera illustration on aged vellum, stylised figures without perspective, gold-leaf highlights, deep red and lapis-blue pigments, ornamental borders with vines, cracked parchment texture.",
		Category: "art", Suits: "підходить для середньовіччя, легенд і церковної історії", Palette: [3]string{"#EFE3C2", "#7A1F1F", "#2F4A7A"}},
	{ID: "ukiyo-e", Label: "Укійо-е",
		Prompt:   "Ukiyo-e woodblock print: Edo-period Japanese style, bold flowing outlines, flat areas of colour, Prussian-blue waves and skies, subtle wood-grain texture, stylised clouds and patterns, elegant asymmetrical composition.",
		Category: "art", Suits: "підходить для японських легенд, самураїв і природи", Palette: [3]string{"#F2E6CE", "#1F4E79", "#C0392B"}},
	{ID: "petrykivka", Label: "Петриківський розпис",
		Prompt:   "Ukrainian Petrykivka folk painting: decorative flowing floral ornaments of viburnum berries, feather-like leaves and fantastic birds, single brush-stroke petals, vivid red, orange, yellow and green on a black or cream background, people and places stylised as folk art.",
		Category: "art", Suits: "підходить для українських казок, легенд і свят", Palette: [3]string{"#111111", "#E63946", "#F4A261"}},
	{ID: "art-deco", Label: "Ар-деко постер",
		Prompt:   "Art-deco poster: 1920s–30s travel-poster illustration, bold geometric shapes, streamlined forms, strong symmetry and sunburst motifs, gold, black and deep teal palette, flat colour areas with smooth gradients, elegant luxurious mood.",
		Category: "art", Suits: "підходить для історій 1920–30-х, розкоші й подорожей", Palette: [3]string{"#0B132B", "#C9A227", "#E0E1DD"}},
	{ID: "linocut", Label: "Ліногравюра",
		Prompt:   "Linocut print: hand-carved relief print look, bold black carved lines and gouge marks, high contrast, one or two flat ink colours, visible ink texture and paper grain, strong graphic silhouettes.",
		Category: "art", Suits: "підходить для притч, фольклору й історичних тем", Palette: [3]string{"#F2EBDD", "#1B1B1B", "#B23A48"}},
	{ID: "stained-glass", Label: "Вітраж",
		Prompt:   "Stained glass: luminous cathedral window, jewel-toned glass pieces joined by thick dark lead lines, light shining through vivid cobalt, ruby and amber glass, stylised figures and symbols, subtle glass texture and glow.",
		Category: "art", Suits: "підходить для релігії, легенд і лицарських історій", Palette: [3]string{"#1B3A6B", "#C1121F", "#F4B400"}},
	{ID: "charcoal", Label: "Вугілля",
		Prompt:   "Charcoal drawing: expressive smudged charcoal on textured paper, deep velvety blacks, soft blended greys, bold gestural strokes, dramatic light and shadow, raw emotional atmosphere, monochrome.",
		Category: "art", Suits: "підходить для драм, воєнних історій і біографій", Palette: [3]string{"#1A1A1A", "#6B6B6B", "#E6E2DA"}},
	{ID: "sumi-e", Label: "Туш сумі-е",
		Prompt:   "Sumi-e ink wash: East Asian brush painting with flowing black ink on rice paper, varied wet and dry strokes, soft grey washes, generous empty space, minimal composition, a single small red accent, meditative calm.",
		Category: "art", Suits: "підходить для філософії, дзену й азійських легенд", Palette: [3]string{"#F5F1E6", "#1C1C1C", "#B0413E"}},
	{ID: "gouache-book", Label: "Гуаш для книжки",
		Prompt:   "Gouache picture book: opaque matte gouache illustration, soft chalky textures and visible brushwork, warm gentle palette, simple charming characters, cosy storybook compositions, classic children's book feel.",
		Category: "art", Suits: "підходить для дитячих книжок і казок на ніч", Palette: [3]string{"#F7E1D7", "#4A5759", "#EDAFB8"}},
	{ID: "crayon", Label: "Дитячий малюнок",
		Prompt:   "Crayon kids drawing: naive wax-crayon drawing as if made by a child, waxy uneven strokes, bright primary colours, wobbly outlines, simple proportions, textured white paper, joyful innocent charm.",
		Category: "art", Suits: "підходить для дитячих історій, гумору й спогадів", Palette: [3]string{"#FFFFFF", "#FF9F1C", "#2EC4B6"}},
	{ID: "black-figure", Label: "Давньогрецька кераміка",
		Prompt:   "Ancient Greek black-figure pottery painting: silhouetted figures in glossy black on terracotta orange, fine incised detail lines, stylised profiles and poses, meander and palmette ornament bands, flat decorative composition like a painted amphora.",
		Category: "art", Suits: "підходить для міфології й античної історії", Palette: [3]string{"#C8702E", "#1A1410", "#E8C9A0"}},
	{ID: "dark-fantasy", Label: "Темне фентезі",
		Prompt:   "Dark fantasy painting: moody painterly illustration, gothic ruins, twisted forests and eerie creatures, cold fog, desaturated palette with sickly green and deep purple accents, dramatic rim light, ominous atmosphere.",
		Category: "art", Suits: "підходить для хорору, фольклору й легенд про чудовиськ", Palette: [3]string{"#0F0F0F", "#4B3F72", "#A3A380"}},
	{ID: "tarot-nouveau", Label: "Таро в стилі модерн",
		Prompt:   "Art-nouveau tarot illustration: elegant flowing linework, ornate floral frames and halos, symbolic figures and celestial motifs, muted gold, deep blue and cream palette, flat decorative colour with fine detail, mystical card-art composition.",
		Category: "art", Suits: "підходить для езотерики, міфів і психології", Palette: [3]string{"#1D2D44", "#D4A373", "#F1E3D3"}},

	// ---- effects ---------------------------------------------------------------------------------
	{ID: "cosmic-glow", Label: "Космічне сяйво",
		Prompt:   "Cosmic mystic glow: ethereal glowing figures and symbols in deep space, nebula clouds in violet, magenta and teal, luminous particles, sacred-geometry light patterns, strong rim light and bloom, dreamy spiritual atmosphere.",
		Category: "effects", Suits: "підходить для езотерики, містики й духовних тем", Palette: [3]string{"#0B0628", "#7B2FF7", "#F9C80E"}},
	{ID: "voxel", Label: "Воксельний світ",
		Prompt:   "Voxel world: everything built from small cubic blocks, blocky characters and terrain, crisp cube edges, bright saturated game colours, soft ambient occlusion, sunny sky, playful sandbox-game look.",
		Category: "effects", Suits: "підходить для ігрових тем, дитячих пригод і пояснень", Palette: [3]string{"#5B8C2A", "#8B5A2B", "#7EC8E3"}},
	{ID: "low-poly", Label: "Низькополігональне 3D",
		Prompt:   "Low-poly 3D: faceted geometric models with flat-shaded triangles, clean pastel-to-vivid gradients, minimal textures, soft studio lighting and gentle shadows, simple stylised landscapes and characters, modern indie-game aesthetic.",
		Category: "effects", Suits: "підходить для пояснень, ігрових і техно-тем", Palette: [3]string{"#264653", "#2A9D8F", "#E9C46A"}},
	{ID: "cyberpunk", Label: "Неоновий кіберпанк",
		Prompt:   "Neon cyberpunk: rain-soaked futuristic megacity at night, glowing magenta and cyan neon signs without readable text, holograms, reflective wet streets, haze and volumetric light, high-tech gear, gritty high-contrast cinematic mood.",
		Category: "effects", Suits: "підходить для технологій, майбутнього й трилерів", Palette: [3]string{"#0D0221", "#FF2A6D", "#05D9E8"}},
	{ID: "vaporwave", Label: "Вейпорвейв",
		Prompt:   "Vaporwave: dreamy retro-digital aesthetic, pastel pink, cyan and purple gradients, 1980s–90s computer graphics, glowing grid horizons, classical marble busts, palm trees and sunsets, chromatic aberration and soft glow.",
		Category: "effects", Suits: "підходить для ностальгії, інтернет-культури й музики", Palette: [3]string{"#FF71CE", "#01CDFE", "#B967FF"}},
	{ID: "analog-horror", Label: "Аналоговий хорор VHS",
		Prompt:   "Analog horror VHS: degraded 1990s camcorder footage, tracking lines, colour bleeding, heavy noise and interlacing, washed-out greenish tones, low-light grain, unsettling empty spaces and half-seen figures, found-footage dread.",
		Category: "effects", Suits: "підходить для хорору, містики й моторошних історій", Palette: [3]string{"#0A0A0A", "#3B5249", "#C9D6DF"}},
}

// StylePrompt returns the prompt text of a style: the custom style wins when
// set, else the built-in prompt ("" for an unknown id).
func StylePrompt(id, custom string) string {
	if c := strings.TrimSpace(custom); c != "" {
		return c
	}
	if s, ok := StyleByID(id); ok {
		return s.Prompt
	}
	return ""
}

// StyleByID finds a built-in style.
func StyleByID(id string) (StyleDef, bool) {
	for _, s := range Styles {
		if s.ID == id {
			return s, true
		}
	}
	return StyleDef{}, false
}

// styleDirection is the planner direction of a project's built-in style
// ("" when it has none).
func styleDirection(in Input) string {
	if s, ok := styleOf(in); ok {
		return s.Direction
	}
	return ""
}

// letteringRule swaps the shot writer's "no text in the image" sentences for
// the style's TextPolicy (map labels, years, price tags), naming the
// language the labels are written in.
func letteringRule(p *Project, image []string) []string {
	s, ok := styleOf(p.Input)
	if !ok || s.TextPolicy == "" {
		return image
	}
	lang := languageName(p.Input.Language)
	if lang == "" {
		lang = "the language of the voice-over"
	}
	policy := s.TextPolicy + " Write every such label in " + lang + " and give its exact words in quotes."
	out := make([]string, len(image))
	for i, l := range image {
		for _, no := range []string{
			"No text, captions, speech bubbles or logos in the image.",
			"No text, captions, labels or logos in the image.",
		} {
			l = strings.Replace(l, no, policy, 1)
		}
		out[i] = l
	}
	return out
}
