# Збірка YT Cartell Studio для macOS

Коротко: на Mac (Apple Silicon або Intel) став Homebrew, Xcode Command Line Tools і Go, клонуй
репозиторій (основний чи свій форк) і запусти `bash build-mac.sh`. Скрипт збере програму для обох
типів процесорів, запакує її в zip і порахує SHA-256. З `--upload` (потрібен GitHub CLI) він ще й
докладе файли до релізу на GitHub — звідти їх забирає вбудоване оновлення студії.

> На Windows збірку для Mac зробити не можна: графічна бібліотека Gio на macOS потребує cgo (компілятора
> C від Apple), тож збирати треба саме на Mac.

## 1. Що встановити (один раз)

1. **Homebrew** — менеджер програм для macOS:

   ```sh
   /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
   ```

   Наприкінці інсталятор покаже дві команди «Next steps» (для Apple Silicon це
   `eval "$(/opt/homebrew/bin/brew shellenv)"`) — виконай їх, щоб `brew` з'явився в Терміналі.

2. **Xcode Command Line Tools** — компілятор `clang`, `lipo`, `codesign`, `iconutil`:

   ```sh
   xcode-select --install
   ```

3. **Go 1.27 або новіший**:

   ```sh
   brew install go
   go version          # має показати go1.27 або вище
   ```

4. **GitHub CLI** (лише для `--upload`) — щоб завантажувати файли в реліз свого репозиторію:

   ```sh
   brew install gh
   gh auth login       # GitHub.com → HTTPS → увійти через браузер
   ```

   Акаунт має право запису в репозиторій, куди публікуєш релізи (`--repo власник/назва`).

5. **FFmpeg** — без нього студія не монтує відео (для самої збірки не потрібен):

   ```sh
   brew install ffmpeg
   ```

6. **Локальне розпізнавання мови** (необов'язково) — whisper.cpp:

   ```sh
   brew install whisper-cpp
   ```

   Студія сама знайде `whisper-cli` у `/opt/homebrew/bin` (Intel — `/usr/local/bin`), а модель
   докачає в «Налаштування → Розпізнавання мови → Локально».

## 2. Клонуй репозиторій

```sh
git clone https://github.com/<власник>/<репозиторій>.git yt-cartell-studio
cd yt-cartell-studio        # якщо код лежить у підпапці GO — перейди в неї
```

Якщо репозиторій уже є — просто онови його: `git pull`.

## 3. Збери

```sh
bash build-mac.sh              # лише зібрати: файли з'являться в dist/mac
bash build-mac.sh --upload     # зібрати й докласти до релізу v<версія> на GitHub
```

Корисні параметри:

| Параметр | Що робить |
|---|---|
| `--version 2.0.1` | інша версія (типово — та, що записана в `internal/version/version.go`) |
| `--arch arm64` | лише Apple Silicon (`amd64` — лише Intel, `both` — обидві, типово) |
| `--upload` | докласти zip-и й `SHA256SUMS.txt` до релізу (`gh release upload --clobber`) |
| `--repo власник/назва` | інший публічний репозиторій для релізів (форк); оновлення в зібраній програмі теж читатиме його |
| `--out папка` | інша папка для результатів |

Що вийде в `dist/mac`:

| Файл | Для кого |
|---|---|
| `YT-Cartell-Studio-<версія>-macos-arm64.zip` | Mac з Apple Silicon (M1–M4 і новіші) |
| `YT-Cartell-Studio-<версія>-macos-x64.zip` | Mac з процесором Intel |
| `YT-Cartell-Studio-<версія>-macos-universal.zip` | обидва (з'являється, коли зібралися обидві архітектури) |
| `SHA256SUMS.txt` | контрольні суми; з `--upload` — разом із рядком Windows-збірки з релізу |

Усередині кожного zip — `YT Cartell Studio.app` з іконкою, версією та мінімальною macOS 13 Ventura
(Go 1.27 старіші macOS не підтримує).

**Порядок публікації.** Реліз створює Windows-скрипт: спершу на Windows виконай
`.\release.ps1 -Notes нотатки.md`, а потім на Mac — `bash build-mac.sh --upload`. Якщо релізу ще немає,
скрипт зупиниться й підкаже. Без рядка в `SHA256SUMS.txt` студія нічого не встановлює.

Якщо Intel-збірка на Mac з Apple Silicon не вдалася, скрипт пропустить її й збере решту — тоді збери
x64 на Intel-маку командою `bash build-mac.sh --arch amd64 --upload`.

## 4. Перший запуск: програма не підписана Apple

Студія не нотаризована в Apple, тому macOS при першому запуску попередить про «невідомого розробника».

1. Розпакуй zip (подвійний клік) і перетягни **YT Cartell Studio.app** у «Програми» (Applications).
   Оновлення замінює програму на місці, тож папка має бути доступна для запису — «Програми» на акаунті
   адміністратора підходить.
2. Відкрий програму **правим кліком (або Control-кліком) → «Відкрити» → «Відкрити»**. На macOS 15
   Sequoia і новіших: спробуй відкрити звичайно, потім «Системні параметри → Приватність і безпека» →
   внизу «Усе одно відкрити».
3. Або одним рядком у Терміналі зніми позначку карантину:

   ```sh
   xattr -dr com.apple.quarantine "/Applications/YT Cartell Studio.app"
   ```

Далі студія запускається як звичайно. Наступні версії вона ставить сама: пункт «Оновлення» на лівій
панелі → «Завантажити й встановити» → «Перезапустити зараз» — без цих кроків.

При першому запуску macOS може спитати дозвіл на папку «Документи»: там студія тримає проєкти
(`~/Documents/YT Cartell Studio`) — дозволь. Налаштування й журнал лежать у
`~/Library/Application Support/YT Cartell Studio`.

Коли вперше натиснеш мікрофон у «Новій історії» (диктування), macOS спитає дозвіл на мікрофон —
дозволь (текст запиту — `NSMicrophoneUsageDescription` у `build-mac.sh`). Відмовив — «Системні
параметри → Конфіденційність і безпека → Мікрофон». FFmpeg студія може поставити й сама:
«Налаштування → FFmpeg → Встановити автоматично» (через Homebrew, а без нього — збірки martin-riedl.de).

## 5. Якщо щось пішло не так

| Повідомлення | Що робити |
|---|---|
| `xcrun: error: invalid active developer path` | `xcode-select --install` |
| `Go 1.2x is too old` | `brew upgrade go` |
| `the release vX.Y.Z does not exist` | спершу опублікуй Windows-збірку: `.\release.ps1 -Notes …` |
| `gh is not signed in` / `HTTP 404` | `gh auth login` акаунтом із правом запису в репозиторій релізів |
| Студія пише «FFmpeg не знайдено» | `brew install ffmpeg` (студія шукає в `/opt/homebrew/bin` і `/usr/local/bin`) |
| «Програму пошкоджено, її не можна відкрити» | зняти карантин командою `xattr` з розділу 4 |
