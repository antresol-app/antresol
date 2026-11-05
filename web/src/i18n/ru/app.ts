// Russian source strings (app group: app shell, routes, session, api, setup, profile picker, offline home).
export default {
  'app.api.bookFileMissing': 'Файл книги не найден на сервере.',
  'app.skipToContent': 'Перейти к содержимому',
  'app.failedTryAgain': 'Не получилось. Попробуйте ещё раз.',
  // routes: offline screen, placeholders
  'app.offline.hint': 'Проверьте, что сервер Antresol включён и вы в той же сети.',
  'app.notFound.title': 'Страница не найдена',
  'app.notFound.text': 'Такой страницы нет. Вернитесь на Главную.',
  // a lazily loaded section that failed
  'app.chunk.failed': 'Не удалось открыть раздел',
  'app.chunk.failedHint': 'Проверьте подключение к серверу и попробуйте ещё раз.',
  // session
  'app.session.remotePasswordless':
    'Для входа не из дома у профиля должен быть пароль. Попросите администратора задать его в Управлении → Профили.',
  // first run
  'app.setup.welcome': 'Добро пожаловать в Antresol',
  'app.setup.lead': 'Фильмы, сериалы, музыка и книги для всей семьи — на вашем сервере.',
  'app.setup.remote':
    'Первую настройку можно сделать только из домашней сети. Откройте Antresol с компьютера или телефона, подключённого к домашнему Wi‑Fi.',
  'app.setup.name': 'Ваше имя',
  'app.setup.namePlaceholder': 'Например, Александр',
  'app.setup.nameRequired': 'Как вас зовут?',
  'app.setup.passwordPlaceholder': 'Пароль администратора',
  'app.setup.passwordRequired': 'Придумайте пароль',
  'app.setup.passwordShort': 'Не короче 6 символов',
  'app.setup.passwordHint': 'Не короче 6 символов. С этим профилем вы будете добавлять фильмы и управлять семьёй.',
  'app.setup.continue': 'Продолжить',
  'app.setup.footnote': 'Профили для близких можно добавить позже в разделе «Управление».',
  // «Кто смотрит?»
  'app.profiles.whoIsWatching': 'Кто смотрит?',
  'app.profiles.loading': 'Загрузка профилей',
  'app.profiles.loadFailed': 'Не удалось загрузить профили',
  'app.profiles.withPassword': ', с паролем',
  'app.profiles.enterPasswordToContinue': 'Введите пароль, чтобы продолжить',
  'app.profiles.enterPassword': 'Введите пароль',
  'app.profiles.signIn': 'Войти',
  'app.profiles.signInFailed': 'Не удалось войти',
  // «На этом устройстве» (OfflineHome)
  'app.offlineHome.title': 'На этом устройстве',
  'app.offlineHome.docTitle': 'На этом устройстве — {app}',
  'app.offlineHome.docTitleOffline': 'Нет связи — {app}',
  'app.offlineHome.openFailed':
    'Не удалось открыть сохранённое. Удалите его с устройства и сохраните снова, когда сервер будет доступен.',
  'app.offlineHome.saved': 'Сохранено: {size}',
  'app.offlineHome.appOnDevice': '{app} на устройстве: {size}',
  'app.offlineHome.hint': 'Сохраняйте альбомы, книги и аудиокниги, чтобы слушать и читать без связи с сервером.',
  'app.offlineHome.nothingSaved': 'Пока ничего не сохранено',
  'app.offlineHome.nothingSavedHint': 'На странице альбома, книги или манги выберите «На это устройство» → «Сохранить офлайн».',
  'app.offlineHome.banner': 'Нет связи с сервером — доступно сохранённое на этом устройстве',
  'app.offlineHome.nothingOnDevice': 'На устройстве ничего не сохранено',
} as const
