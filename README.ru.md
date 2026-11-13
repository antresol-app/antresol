# Antresol

**Домашний медиасервер для фильмов, сериалов, музыки, аудиокниг, книг и
комиксов, которые у вас уже есть.** Укажите ему свои папки, откройте его в
браузере, и у всей семьи будет одна медиатека с профилями, прогрессом,
который переходит с устройства на устройство, и плеером, который работает и
дома, и вне дома.

[English](README.md)

![Главный экран: полки фильмов и сериалов](docs/images/home.png)

> **Честно о проекте.** Antresol поддерживает один человек с помощью ИИ.
> Никакого контента проект не предоставляет: Antresol играет только файлы из
> ваших собственных папок и подбирает к ним описания и обложки. Добавляйте
> только то, на что у вас есть права. Приложение для iPhone, iPad и Mac
> закрытое и работает с сервером через тот же публичный API, что и
> веб-интерфейс; сервер и веб-интерфейс в этом репозитории открыты (см.
> [Лицензия](#лицензия)).

## Что он умеет

- **Фильмы и сериалы** с постерами, описаниями, актёрами и сезонами. Играет
  в браузере: исходный файл, если браузер его понимает, иначе ремукс или
  перекодирование, с аппаратным декодированием, где его умеет ffmpeg хоста
  (Intel Quick Sync, VideoToolbox). Звуковые дорожки, субтитры, пропуск
  заставки и титров, ограничение качества для зрителей вне дома.
- **Музыка**: альбомы, исполнители, плейлисты, CUE-образы, синхронный текст,
  почти бесшовное воспроизведение, выравнивание громкости. Subsonic API для
  музыкальных приложений.
- **Книги, аудиокниги, манга и комиксы**: читалка EPUB, FB2 и PDF с
  выделениями и заметками, плеер аудиокниг с главами и таймером сна,
  читалка комиксов с чтением справа налево. OPDS-каталог для приложений-читалок.
- **Профили** для всей семьи: пароли, детские профили, которые видят только
  разрешённое, экранное время.
- **Офлайн**: альбом, книгу или том можно сохранить на устройство из
  веб-приложения и открыть без связи; позиции дойдут до сервера, когда связь
  вернётся.
- **Метаданные** из TMDB, MusicBrainz, Cover Art Archive и Open Library.
  Каждый источник можно выключить.
- Интерфейс на русском и английском.
- Один самодостаточный бинарь на Go со встроенным веб-интерфейсом, база
  SQLite, других сервисов не нужно.

<p>
  <img src="docs/images/title.png" alt="Страница фильма" width="49%">
  <img src="docs/images/music.png" alt="Альбом" width="49%">
</p>
<p>
  <img src="docs/images/books.png" alt="Полка книг" width="49%">
  <img src="docs/images/profiles.png" alt="Выбор профиля" width="49%">
</p>
<p><img src="docs/images/phone.png" alt="Главный экран на телефоне, 375 px" width="200"></p>

## Установка через Docker

Нужен Linux-хост с Docker и Compose и медиатека в одной папке:

```
/srv/media/
  movies/      Название фильма (2019)/Название фильма (2019).mkv
  series/      Сериал/Season 01/Сериал S01E01.mkv
  music/       Исполнитель/2019 - Альбом/01 - Трек.flac
  books/
    ebooks/      Автор - Название.epub
    audiobooks/  Автор - Название/*.mp3  (или один .m4b)
    manga/       Серия/Том 01.cbz
```

Папки могут называться иначе — это задаётся в `.env` ниже. Скачивать
репозиторий не нужно: создайте папку, например `~/antresol`, и положите в неё
два файла.

`compose.yml` (тот же, что [`compose.yml`](compose.yml) в репозитории):

```yaml
# Antresol Server with Docker Compose (see README.md, "Install with Docker").
#
# Put this file and a .env next to each other (the README has both in full),
# set MEDIA_DIR in .env to your media folder on the host, then:
#
#   docker compose up -d
#
# and open http://<host>:8420 from your home network.
#
# Paths: MEDIA_DIR is a path on the HOST. Inside the container it is always
# /media, and the *_DIR folder names below are relative to it. Do not put
# host paths into VAULT_* variables: the server sees only the container.
#
# To build the image from a checkout instead of pulling it:
#   docker build -t ghcr.io/antresol-app/antresol-server:latest .
services:
  antresol:
    image: ghcr.io/antresol-app/antresol-server:latest
    container_name: antresol
    restart: unless-stopped
    # stops ffmpeg, then waits for open requests
    stop_grace_period: 30s
    ports:
      - "8420:8420"
    env_file:
      - path: .env
        required: false
    environment:
      VAULT_DATA_DIR: /config
      VAULT_MEDIA_ROOT: /media
      # folder names inside MEDIA_DIR; change them in .env if yours differ
      VAULT_MOVIES_DIR: ${MOVIES_DIR:-movies}
      VAULT_SERIES_DIR: ${SERIES_DIR:-series}
      VAULT_MUSIC_DIR: ${MUSIC_DIR:-music}
      VAULT_BOOKS_DIR: ${BOOKS_DIR:-books}
      VAULT_EBOOKS_DIR: ${EBOOKS_DIR:-books/ebooks}
      VAULT_AUDIOBOOKS_DIR: ${AUDIOBOOKS_DIR:-books/audiobooks}
      VAULT_COMICS_DIR: ${COMICS_DIR:-books/manga}
      # HLS segments of a transcode: on the media disk, not in the config volume
      VAULT_TRANSCODE_DIR: /media/tmp/antresol-hls
    volumes:
      - antresol-config:/config
      # read-write: fetched subtitles, transcode segments, offline copies
      - ${MEDIA_DIR:-./media}:/media
    # The image has no GPU video driver yet, so it decodes on the CPU; run the
    # binary on the host for hardware decoding.

volumes:
  antresol-config:
```

`.env`:

```sh
# Папка медиатеки НА ХОСТЕ. В контейнере она всегда /media.
MEDIA_DIR=/srv/media

# Имена папок внутри MEDIA_DIR — только если у вас они другие:
# MOVIES_DIR=movies
# SERIES_DIR=series
# MUSIC_DIR=music
# EBOOKS_DIR=books/ebooks
# AUDIOBOOKS_DIR=books/audiobooks
# COMICS_DIR=books/manga

# Часовой пояс
TZ=Europe/Moscow

# По желанию: описания фильмов и сериалов (бесплатный ключ:
# themoviedb.org/settings/api). Без него названия, постеры и описания
# берутся только из ваших файлов.
VAULT_TMDB_KEY=

# За обратным прокси (Nginx Proxy Manager, Caddy, Traefik): IP-адрес прокси,
# как его видит сервер. Без прокси оставьте пустым.
VAULT_TRUSTED_PROXIES=

# По желанию: автоматические субтитры с opensubtitles.com.
# VAULT_OPENSUBTITLES_KEY=
# VAULT_OPENSUBTITLES_USER=
# VAULT_OPENSUBTITLES_PASSWORD=
```

Затем в этой папке:

```sh
docker compose up -d
docker compose logs -f     # сканирование напишет, какую папку не нашло
```

Откройте `http://<хост>:8420` из домашней сети и создайте первого
администратора. Сервер просматривает папки при старте и каждые 15 минут
(или сразу: Управление → Настройки в веб-интерфейсе).

- **Пути.** `MEDIA_DIR` — единственный путь на хосте. Сервер работает внутри
  контейнера и видит медиатеку в `/media`; имена `*_DIR` отсчитываются от неё.
  Не задавайте `VAULT_MEDIA_ROOT`, `VAULT_MOVIES_DIR` или
  `VAULT_TRANSCODE_DIR` путями хоста вроде `/tank/media/...`: в контейнере
  такой папки нет (`stat ...: no such file or directory`,
  `mkdir ...: permission denied`). Недостающие папки внутри медиатеки
  создаются пустыми: все при новой установке, папки музыки и книг и потом.
- Контейнер работает от uid/gid 1000, ему нужна запись в папку медиатеки
  (скачанные субтитры, временные файлы перекодирования в `tmp/antresol-hls`).
  Если файлы принадлежат другому пользователю, дайте uid 1000 право записи
  или добавьте сервису `user: "<uid>:<gid>"`. Настройки и база лежат в томе
  `antresol-config`.
- В образе пока нет драйвера видеокарты, поэтому он перекодирует на
  процессоре. Для аппаратного декодирования запускайте бинарь прямо на хосте
  (см. ниже): ffmpeg возьмёт драйверы хоста (`VAULT_HWACCEL`).
- Для доступа не из дома поставьте Antresol за обратный прокси с HTTPS и
  укажите адрес прокси в `VAULT_TRUSTED_PROXIES`. Нужный адрес виден в логе:
  `request from <ip> carries X-Forwarded-For ... does not list it`. Профиль
  без пароля входит только из домашней сети.
- Любая настройка — переменная окружения, её можно вписать в `.env`; больше
  их в [`.env.example`](.env.example), сборка описана в
  [docs/build-tags.md](docs/build-tags.md).

**Обновление с версии 0.3.0.** На старый образ новые версии больше не выходят:
в прежнем compose-файле или команде `docker run` замените образ на тот, что в
`compose.yml`. Оставьте тот же `/config` и медиатеку по тому же пути в
контейнере: в базе записаны полные пути к файлам, и тогда настройки, профили и
прогресс остаются как были. С опубликованным образом больше ничего менять не
надо. Образ, собранный вами из старого Dockerfile, сам задавал
`VAULT_MEDIA_ROOT` с другим путём, а без него сервер теперь берёт `/media`:
задайте его равным пути, куда подключена медиатека. Пока он не задан, новые файлы
не находятся, а уже известные сервер не теряет. Если вместо этого подключить
медиатеку по новому пути, пути изменятся: медиатека проиндексируется заново,
каждая позиция появится второй раз без прогресса, а старые записи останутся и
перестанут воспроизводиться.

Запуск с этим `compose.yml` как есть — это новая установка: его том
`antresol-config` новый и пустой, поэтому профилей и прогресса в нём нет. Чтобы
сохранить их, укажите в нём `/config` на прежнюю папку; тогда пути изменятся, как
описано выше, потому что он ищет в `movies/`, а не в `media/movies/`.

## Сборка из исходников

Нужны Go 1.27, Node 22 (см. `.nvmrc`), а также `ffmpeg` и `ffprobe` версии
6.1 или новее в `PATH`. ffmpeg 6.0 и старше не знает
`-readrate_initial_burst`, а его передаёт каждый ремукс и каждое
перекодирование, так что с ним работает только прямое воспроизведение. В
Docker-образе ffmpeg уже новее.

```sh
make build                      # веб-бандл, затем bin/vault со встроенным бандлом
VAULT_DATA_DIR=./data VAULT_MEDIA_ROOT=/srv/media bin/vault
```

`make test` запускает тесты Go, `make test-web` — юнит-тесты веба,
`make e2e` — браузерные тесты (Playwright). `docker build .` собирает образ
сервера. `vault` — кодовое имя проекта: оно осталось в бинаре, переменных
`VAULT_*` и файле базы.

## Приложения

- **iPhone, iPad и Mac**: приложение Antresol в App Store. Пока оно закрытое
  и работает через публичный API сервера. (Его читалка книг собрана из
  файлов этого репозитория под MPL-2.0; см. [LICENSING.md](LICENSING.md).)
- **Музыка**: любой Subsonic-клиент. Создайте пароль для приложений в меню
  профиля и укажите адрес сервера, имя профиля и этот пароль.
- **Читалки**: любое OPDS-приложение, адрес `<сервер>/opds`, тот же пароль
  для приложений.

## Участие

Мы рады правкам на английском и на русском. Сначала прочтите
[CONTRIBUTING.md](CONTRIBUTING.md): подпись (DCO), что входит в проект,
правила интерфейса и как мы относимся к работе с помощью ИИ. Перед большими
или визуальными изменениями откройте issue. Уязвимости — по
[SECURITY.md](SECURITY.md). Все участники соблюдают
[Кодекс поведения](CODE_OF_CONDUCT.md). Как принимаются решения —
[GOVERNANCE.md](GOVERNANCE.md).

## Лицензия

Antresol Server — свободная программа под лицензией
[GNU Affero General Public License v3.0 или новее](LICENSE)
(`AGPL-3.0-or-later`). Читалка для приложения и переводы
(`web/reader-native.html`, девять файлов в `web/src/reader` и `web/src/i18n`)
распространяются под [Mozilla Public License 2.0](web/src/reader/LICENSE)
(`MPL-2.0`). Точный список — в [LICENSING.md](LICENSING.md), сторонний код —
в [NOTICE](NOTICE) и [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).

Каждый сервер предлагает свой исходный код: на экранах входа и в меню
профиля («О сервере») есть ссылка «Исходный код · AGPL», а `GET /api/about`
отвечает версией, коммитом, лицензией и ссылкой. Сборки релизов ссылаются на
свой тег; сборка из исходников ссылки не показывает, пока вы её не зададите.
Если вашим изменённым сервером пользуются другие, AGPL просит предложить им
ваш исходный код: укажите, где он, в `VAULT_SOURCE_URL` (или соберите с
`make SOURCE_URL=<адрес>`).

Название «Antresol» и логотип этими лицензиями не охватываются; см.
[TRADEMARKS.md](TRADEMARKS.md).

Этот продукт использует TMDB API, но не одобрен и не сертифицирован TMDB.
