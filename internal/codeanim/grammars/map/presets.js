// "map" grammar presets: historical regions, hand-drawn river lines that double as borders, a small gazetteer and the colour palette.
// All coordinates are [lon, lat] (WGS84). Region polygons are CLIPPED TO LAND at render time, so their sea-side edges can be loose:
// only the land borders need care. Borders shared by two presets use the same point arrays, so neighbours meet without gaps.
// Approximations drawn for a history short, not for a court of law. Public domain / MIT like the rest of the grammar.
(() => {
// Chaikin smoothing so the hand-placed river reads as a river and the borders that follow it stay identical
const smooth = (pts, n = 2) => { let p = pts; for (let k = 0; k < n; k++) { const o = [p[0]];
  for (let i = 0; i < p.length - 1; i++) { const a = p[i], b = p[i + 1]; o.push([a[0] * 0.75 + b[0] * 0.25, a[1] * 0.75 + b[1] * 0.25], [a[0] * 0.25 + b[0] * 0.75, a[1] * 0.25 + b[1] * 0.75]); }
  o.push(p[p.length - 1]); p = o; } return p.map(q => [Math.round(q[0] * 1000) / 1000, Math.round(q[1] * 1000) / 1000]); };
const rev = a => a.slice().reverse();

// Mississippi, source (Itasca) -> Baton Rouge; split there because the Isle of Orleans sits east of the river below it.
const MISS_N = smooth([[-95.21, 47.24], [-94.88, 47.47], [-94.3, 47.42], [-93.53, 47.24], [-93.7, 46.53], [-94.2, 46.36], [-94.36, 45.98], [-94.16, 45.56], [-93.57, 45.3],
  [-93.26, 44.98], [-92.85, 44.74], [-92.53, 44.56], [-92.03, 44.38], [-91.64, 44.05], [-91.24, 43.81], [-91.15, 43.05], [-90.66, 42.5], [-90.19, 41.84], [-90.58, 41.52],
  [-91.04, 41.42], [-91.11, 40.81], [-91.39, 40.4], [-91.41, 39.94], [-91.36, 39.71], [-91.05, 39.45], [-90.18, 38.89], [-90.19, 38.63], [-89.82, 37.91], [-89.52, 37.31],
  [-89.18, 37.0], [-89.53, 36.59], [-89.66, 36.19], [-90.07, 35.15], [-90.59, 34.53], [-91.03, 33.85], [-91.07, 33.39], [-91.17, 32.8], [-90.9, 32.35], [-91.42, 31.56],
  [-91.62, 31.0], [-91.6, 30.85], [-91.19, 30.45]]);
const i31 = MISS_N.reduce((b, p, i) => Math.abs(p[1] - 31) < Math.abs(MISS_N[b][1] - 31) ? i : b, 0);   // where the river crosses 31°N
const MISS_A = MISS_N.slice(0, i31 + 1), MISS_B = MISS_N.slice(i31);   // source..31N, 31N..Baton Rouge
const P31 = MISS_N[i31];
const MISS_S = smooth([[-91.19, 30.45], [-90.99, 30.1], [-90.4, 29.97], [-90.07, 29.95], [-89.9, 29.8], [-89.7, 29.6], [-89.4, 29.3], [-89.25, 29.15], [-89.1, 29.0]]);
// Isle of Orleans northern edge (Baton Rouge -> lakes -> sea)
const ORLEANS = [[-91.19, 30.45], [-90.9, 30.33], [-90.5, 30.25], [-90.0, 30.18], [-89.7, 30.17], [-89.4, 30.05], [-89.0, 29.9], [-88.6, 29.6]];
// Louisiana's western limit (continental divide, north -> south) and south-western limit (to the Sabine mouth)
const DIVIDE = [[-114.0, 49.0], [-112.8, 47.2], [-111.4, 45.4], [-110.6, 44.4], [-109.4, 43.2], [-107.3, 42.1], [-106.4, 40.6], [-106.1, 39.1], [-105.9, 38.5]];
const SW = [[-105.9, 38.5], [-105.3, 36.6], [-103.0, 36.5], [-100.0, 36.5], [-100.0, 34.56], [-98.5, 34.1], [-97.2, 33.8], [-95.5, 33.88], [-94.04, 33.55], [-94.04, 32.0], [-93.72, 31.0], [-93.75, 30.3], [-93.84, 29.7]];
// USA 1803 northern border (Lake of the Woods -> Great Lakes -> 45°N -> Maine/New Brunswick -> St Croix mouth)
const US_N = [[-95.15, 49.0], [-94.8, 48.7], [-93.5, 48.55], [-92.0, 48.3], [-90.8, 48.2], [-89.6, 48.0], [-88.4, 48.1], [-86.0, 47.6], [-84.8, 46.9], [-84.4, 46.5], [-83.6, 46.1],
  [-82.5, 45.4], [-82.2, 43.6], [-82.5, 42.9], [-83.1, 42.3], [-83.0, 42.0], [-81.0, 42.3], [-79.1, 42.8], [-79.0, 43.3], [-76.8, 43.6], [-76.3, 44.2], [-75.0, 44.9],
  [-74.7, 45.0], [-71.5, 45.0], [-70.9, 45.3], [-70.0, 46.7], [-69.2, 47.45], [-67.8, 47.1], [-67.8, 45.7], [-67.1, 45.1], [-66.9, 44.8]];
const US_S = [P31, [-88.0, 31.0], [-85.0, 31.0], [-84.86, 30.7], [-84.0, 30.65], [-82.2, 30.57], [-82.0, 30.75], [-81.45, 30.71]];   // 31°N + St Marys river

const R = {};
R.louisiana_1803 = { label: { en: 'Louisiana Territory', ru: 'Луизиана', uk: 'Луїзіана', de: 'Louisiana', fr: 'Louisiane', es: 'Luisiana' }, color: 'blue', labelAt: [-99, 41.5],
  polygons: [[...DIVIDE, ...SW.slice(1), [-93.9, 28.0], [-88.4, 28.4], [-88.6, 29.6], ...rev(ORLEANS).slice(1), ...rev(MISS_N).slice(1), [-95.2, 49.0]]] };
R.usa_1803 = { label: { en: 'USA', ru: 'США', uk: 'США', de: 'USA', fr: 'États-Unis', es: 'EE. UU.' }, color: 'teal', labelAt: [-81.5, 37.6],
  polygons: [[...MISS_A, ...US_S.slice(1), [-79.5, 30.4], [-73, 35], [-68, 40.5], [-66.3, 44.4], ...rev(US_N), [-95.2, 47.5]]] };
R.spanish_florida_1803 = { label: { en: 'Florida', ru: 'Флорида', uk: 'Флорида', de: 'Florida', fr: 'Floride', es: 'Florida' }, color: 'orange', labelAt: [-82.4, 28.6],
  polygons: [[...rev(US_S), ...MISS_B.slice(1), ...ORLEANS.slice(1), [-88, 28.5], [-84, 24.0], [-80.5, 24.2], [-79.6, 26.5], [-79.8, 30.5], [-81.45, 30.71]]] };
R.new_spain_1803 = { label: { en: 'Spain', ru: 'Испания', uk: 'Іспанія', de: 'Spanien', fr: 'Espagne', es: 'España' }, color: 'orange', labelAt: [-103.5, 27.5],
  polygons: [[[-126, 42.0], [-111.0, 42.0], [-107.3, 42.1], ...DIVIDE.slice(-3), ...SW.slice(1), [-93.9, 28.0], [-88.5, 25.0], [-86.1, 22.6], [-85.6, 20.8], [-83.5, 16.5],
    [-82.6, 13.5], [-81.0, 11.0], [-79.0, 9.8], [-77.3, 8.9], [-77.2, 7.8], [-78.6, 6.5], [-82, 6], [-92, 12], [-106, 18], [-117, 28], [-126, 36]]] };
R.british_north_america_1803 = { label: { en: 'Britain', ru: 'Британия', uk: 'Британія', de: 'Britannien', fr: 'Grande-Bretagne', es: 'Gran Bretaña' }, color: 'red', labelAt: [-92, 56],
  polygons: [[[-141, 69.8], [-141, 60.3], [-128, 58.0], [-121, 53.0], [-116.5, 50.0], [-114.0, 49.0], [-95.2, 49.0], ...US_N, [-66.3, 44.0], [-55, 42], [-47, 47], [-52.5, 55],
    [-56.5, 58.5], [-59.5, 63.5], [-62, 67.5], [-66.5, 72], [-74.5, 77.0], [-73.5, 79.0], [-64.5, 81.8], [-62.0, 82.6], [-60, 84.0], [-100, 84.5], [-141, 75]]] };
R.thirteen_colonies = { label: { en: 'Thirteen Colonies', ru: 'Тринадцать колоний', uk: 'Тринадцять колоній' }, color: 'red', labelAt: [-77.5, 39],
  polygons: [[[-66.9, 44.8], [-67.8, 45.7], [-67.8, 47.1], [-69.2, 47.45], [-70.0, 46.7], [-71.5, 45.0], [-74.7, 45.0], [-76.5, 43.6], [-79.0, 43.3], [-79.8, 42.0], [-80.5, 40.6],
    [-80.5, 39.7], [-81.5, 38.0], [-82.5, 37.0], [-84.0, 35.0], [-83.5, 33.0], [-82.0, 31.5], [-81.45, 30.71], [-79.5, 30.4], [-73, 35], [-68, 40.5], [-66.3, 44.4]]] };
R.france_metropolitan = { label: { en: 'France', ru: 'Франция', uk: 'Франція', de: 'Frankreich', fr: 'France', es: 'Francia' }, color: 'blue', labelAt: [2.4, 46.6],
  countries: ['FRA'], bbox: [-6, 41, 10, 52] };
R.french_empire_1812 = { label: { en: 'French Empire', ru: 'Французская империя', uk: 'Французька імперія', de: 'Französisches Kaiserreich', fr: 'Empire français' }, color: 'blue', labelAt: [3.0, 47.0],
  countries: ['FRA', 'BEL', 'NLD', 'LUX'], bbox: [-6, 41, 16, 56],
  polygons: [
    [[6.0, 51.85], [6.9, 53.6], [8.5, 53.95], [10.0, 54.1], [10.9, 53.95], [10.8, 53.3], [10.0, 53.0], [9.0, 52.6], [8.3, 52.0], [7.6, 51.3], [7.6, 50.5], [8.2, 49.9], [8.2, 49.0], [7.0, 49.15], [6.4, 49.45], [6.1, 50.1], [6.0, 51.0]],
    [[6.9, 45.85], [7.9, 45.95], [8.6, 45.4], [9.3, 45.1], [10.0, 44.9], [10.5, 44.3], [11.5, 44.0], [12.3, 43.6], [13.0, 42.8], [13.4, 41.8], [13.0, 41.2], [12.0, 40.8], [10.0, 42.5], [9.6, 43.6], [8.5, 44.0], [7.5, 43.7], [7.0, 44.2], [6.6, 45.1]],
    [[13.5, 46.5], [15.5, 46.6], [16.0, 45.9], [16.5, 45.2], [18.5, 43.0], [18.6, 42.4], [17.0, 42.8], [15.0, 44.3], [13.6, 45.2], [13.2, 45.8]]] };
R.uk_1801 = { label: { en: 'Britain', ru: 'Британия', uk: 'Британія', de: 'Britannien', fr: 'Royaume-Uni', es: 'Gran Bretaña' }, color: 'red', labelAt: [-2.2, 53.4], countries: ['GBR', 'IRL'] };
R.spain_europe = { label: { en: 'Spain', ru: 'Испания', uk: 'Іспанія', de: 'Spanien', fr: 'Espagne', es: 'España' }, color: 'orange', labelAt: [-3.6, 40.0], countries: ['ESP'], bbox: [-10, 35, 5, 44.5] };
R.saint_domingue = { label: { en: 'Saint-Domingue', ru: 'Сан-Доминго', uk: 'Сан-Домінго', de: 'Saint-Domingue', fr: 'Saint-Domingue', es: 'Saint-Domingue' }, color: 'blue', labelAt: [-72.6, 19.0], countries: ['HTI'] };
R.haiti = { label: { en: 'Haiti', ru: 'Гаити', uk: 'Гаїті', de: 'Haiti', fr: 'Haïti', es: 'Haití' }, color: 'green', labelAt: [-72.6, 19.0], countries: ['HTI'] };
R.hispaniola = { label: { en: 'Hispaniola', ru: 'Эспаньола', uk: 'Еспаньйола' }, color: 'orange', labelAt: [-71.0, 19.0], countries: ['HTI', 'DOM'] };
R.russian_empire_1812 = { label: { en: 'Russian Empire', ru: 'Российская империя', uk: 'Російська імперія' }, color: 'green', labelAt: [45, 58], countries: ['RUS', 'BLR', 'UKR', 'LTU', 'LVA', 'EST', 'FIN', 'MDA'], bbox: [18, 40, 180, 82] };
R.ottoman_empire_1800 = { label: { en: 'Ottoman Empire', ru: 'Османская империя', uk: 'Османська імперія' }, color: 'purple', labelAt: [34, 39.2],
  countries: ['TUR', 'GRC', 'BGR', 'MKD', 'ALB', 'SYR', 'LBN', 'ISR', 'PSX', 'JOR', 'IRQ', 'EGY', 'LBY', 'TUN', 'CYP', 'XKX', 'SRB', 'BIH', 'MNE', 'ROU'], bbox: [10, 25, 50, 48.5] };
R.dutch_republic = { label: { en: 'Netherlands', ru: 'Нидерланды', uk: 'Нідерланди' }, color: 'orange', labelAt: [5.5, 52.3], countries: ['NLD'], bbox: [3, 50, 8, 54] };
R.portugal_europe = { label: { en: 'Portugal', ru: 'Португалия', uk: 'Португалія' }, color: 'green', labelAt: [-8.2, 39.6], countries: ['PRT'], bbox: [-10, 36, -6, 42.5] };

// Hand rivers (smoothed). NE river ids (e.g. "missouri", "ohio", "rhine", "danube", "nile", "volga") are available too.
const RIVERS = {
  mississippi: { line: [...MISS_N, ...MISS_S.slice(1)], label: { en: 'Mississippi River', ru: 'Миссисипи', uk: 'Міссісіпі', de: 'Mississippi', fr: 'Mississippi', es: 'Misisipi' }, labelAt: [-91.6, 36.5] },
};

// Gazetteer: [lon, lat]
const PLACES = {
  paris: [2.35, 48.86], london: [-0.13, 51.51], madrid: [-3.7, 40.42], lisbon: [-9.14, 38.72], amsterdam: [4.9, 52.37], brussels: [4.35, 50.85], rome: [12.5, 41.9], vienna: [16.37, 48.21],
  berlin: [13.4, 52.52], moscow: [37.62, 55.75], st_petersburg: [30.32, 59.94], warsaw: [21.01, 52.23], istanbul: [28.98, 41.01], venice: [12.33, 45.44], naples: [14.27, 40.85],
  marseille: [5.37, 43.3], bordeaux: [-0.58, 44.84], nantes: [-1.55, 47.22], brest: [-4.49, 48.39], le_havre: [0.11, 49.49], liverpool: [-2.98, 53.41], bristol: [-2.59, 51.45],
  plymouth: [-4.14, 50.37], seville: [-5.98, 37.39], cadiz: [-6.29, 36.53], trafalgar: [-6.03, 36.18], waterloo: [4.4, 50.68], austerlitz: [16.76, 49.15], borodino: [35.82, 55.52],
  moscow_ru: [37.62, 55.75], smolensk: [32.04, 54.78], vilnius: [25.28, 54.69], berezina: [28.5, 54.3], elba: [10.25, 42.78], saint_helena: [-5.72, -15.96], corsica: [9.0, 42.0], ajaccio: [8.74, 41.92],
  new_orleans: [-90.07, 29.95], washington: [-77.04, 38.9], philadelphia: [-75.16, 39.95], new_york: [-74.0, 40.71], boston: [-71.06, 42.36], charleston: [-79.93, 32.78],
  baltimore: [-76.61, 39.29], st_louis: [-90.19, 38.63], pittsburgh: [-79.99, 40.44], quebec: [-71.21, 46.81], montreal: [-73.57, 45.5], halifax: [-63.57, 44.65],
  havana: [-82.37, 23.11], port_au_prince: [-72.34, 18.54], cap_francais: [-72.2, 19.76], santo_domingo: [-69.93, 18.49], kingston: [-76.79, 17.97], bridgetown: [-59.62, 13.1],
  veracruz: [-96.13, 19.17], mexico_city: [-99.13, 19.43], panama: [-79.52, 8.98], cartagena: [-75.51, 10.39], caracas: [-66.9, 10.48], lima: [-77.04, -12.05],
  salvador: [-38.5, -12.97], rio: [-43.2, -22.9], buenos_aires: [-58.38, -34.6],
  elmina: [-1.35, 5.08], goree: [-17.4, 14.67], lagos: [3.39, 6.45], luanda: [13.23, -8.84], cape_town: [18.42, -33.92], zanzibar: [39.2, -6.16], cairo: [31.24, 30.04], alexandria: [29.92, 31.2],
  bombay: [72.88, 19.08], calcutta: [88.36, 22.57], madras: [80.27, 13.08], goa: [73.83, 15.5], colombo: [79.86, 6.93], malacca: [102.25, 2.2], batavia: [106.85, -6.2],
  canton: [113.26, 23.13], macau: [113.54, 22.2], nagasaki: [129.87, 32.75], manila: [120.98, 14.6],
};

// Palette sampled from the reference (flat fills on parchment)
const COLORS = { blue: '#3352a0', royal: '#3352a0', france: '#3352a0', red: '#c22e24', britain: '#c22e24', teal: '#31878f', usa: '#31878f', orange: '#c37224', spain: '#c37224',
  green: '#4f9a36', haiti: '#4f9a36', purple: '#7a4c9b', yellow: '#d8b13a', gold: '#d8b13a', brown: '#8a5a32', gray: '#7b7d80', grey: '#7b7d80', pink: '#c9617f', cyan: '#3aa7c9',
  black: '#26221d', white: '#f4efe2', navy: '#24366e', lightblue: '#5b8fd6', darkred: '#8e1f1a', olive: '#7d8a3a' };

window.MAPGRAM_PRESETS = { regions: R, rivers: RIVERS, places: PLACES, colors: COLORS };
})();
