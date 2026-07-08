// Localization dictionary. Catalan (ca) is primary, Spanish (es) secondary.
// The active language is persisted in localStorage under "lang".
(function () {
  const STRINGS = {
    ca: {
      appTitle: "Planificador de menús",
      tagline: "Planifica els sopars a partir dels dinars i del teu historial",
      navAdd: "Afegir menú",
      navBrowse: "Els meus menús",
      navSuggest: "Suggeriments",

      addHeading: "Afegir un menú setmanal",
      weekStart: "Inici de la setmana (dilluns)",
      uploadLabel: "Puja un PDF o un fitxer de text (.txt, .md) del menú",
      extractBtn: "Extreure",
      startBlank: "Començar en blanc",
      extracting: "Extraient…",
      extractHint:
        "Consell: han de ser PDF amb text real (no escanejats). També pots pujar un .txt o .md.",
      titleLabel: "Títol (opcional)",
      lunch: "Dinar",
      dinner: "Sopar",
      dish: "Plat",
      notes: "Notes",
      saveBtn: "Desar menú",
      saved: "Menú desat!",

      browseHeading: "Menús desats",
      browseEmpty: "Encara no has desat cap menú.",
      viewBtn: "Veure / editar",
      deleteBtn: "Esborrar",
      confirmDelete: "Segur que vols esborrar aquest menú?",

      suggestHeading: "Proposar menú",
      targetDate: "Setmana objectiu (dilluns)",
      lunchUpload: "Puja el menú de dinars (PDF mensual o setmanal)",
      extractLunchesBtn: "Carregar dinars",
      lunchUploadHint:
        "S'extreuen només els dinars (s'ignoren els sopars). Tria la setmana i s'ompliran els dinars corresponents.",
      lunchLoaded: "S'han carregat {n} dinars amb data. Selecciona la setmana objectiu per omplir-los.",
      lunchFilled: "{n} dinars omplerts per a la setmana del {d}.",
      lunchesLabel: "Dinars de la setmana (opcional)",
      constraintsLabel: "Restriccions o preferències",
      constraintsPlaceholder:
        "p. ex.: més verdures, sense peix aquesta setmana, divendres ràpid…",
      proposeDinners: "Proposar sopars",
      generateWeek: "Generar setmana sencera",
      generating: "Generant…",
      suggestHint:
        "Omple els dinars i prem «Proposar sopars», o deixa'ls buits i genera una setmana sencera.",
      saveSuggestion: "Desar aquesta proposta",

      langLabel: "Idioma",
      errorPrefix: "Error",
      loading: "Carregant…",
      requiredDate: "Cal indicar la data d'inici de la setmana.",
      days: ["Dilluns", "Dimarts", "Dimecres", "Dijous", "Divendres", "Dissabte", "Diumenge"],
    },
    es: {
      appTitle: "Planificador de menús",
      tagline: "Planifica las cenas a partir de las comidas y de tu historial",
      navAdd: "Añadir menú",
      navBrowse: "Mis menús",
      navSuggest: "Sugerencias",

      addHeading: "Añadir un menú semanal",
      weekStart: "Inicio de la semana (lunes)",
      uploadLabel: "Sube un PDF o un archivo de texto (.txt, .md) del menú",
      extractBtn: "Extraer",
      startBlank: "Empezar en blanco",
      extracting: "Extrayendo…",
      extractHint:
        "Consejo: deben ser PDF con texto real (no escaneados). También puedes subir un .txt o .md.",
      titleLabel: "Título (opcional)",
      lunch: "Comida",
      dinner: "Cena",
      dish: "Plato",
      notes: "Notas",
      saveBtn: "Guardar menú",
      saved: "¡Menú guardado!",

      browseHeading: "Menús guardados",
      browseEmpty: "Todavía no has guardado ningún menú.",
      viewBtn: "Ver / editar",
      deleteBtn: "Eliminar",
      confirmDelete: "¿Seguro que quieres eliminar este menú?",

      suggestHeading: "Proponer menú",
      targetDate: "Semana objetivo (lunes)",
      lunchUpload: "Sube el menú de comidas (PDF mensual o semanal)",
      extractLunchesBtn: "Cargar comidas",
      lunchUploadHint:
        "Se extraen solo las comidas (se ignoran las cenas). Elige la semana y se rellenarán las comidas correspondientes.",
      lunchLoaded: "Se han cargado {n} comidas con fecha. Selecciona la semana objetivo para rellenarlas.",
      lunchFilled: "{n} comidas rellenadas para la semana del {d}.",
      lunchesLabel: "Comidas de la semana (opcional)",
      constraintsLabel: "Restricciones o preferencias",
      constraintsPlaceholder:
        "p. ej.: más verduras, sin pescado esta semana, viernes rápido…",
      proposeDinners: "Proponer cenas",
      generateWeek: "Generar semana completa",
      generating: "Generando…",
      suggestHint:
        "Rellena las comidas y pulsa «Proponer cenas», o déjalas vacías y genera una semana completa.",
      saveSuggestion: "Guardar esta propuesta",

      langLabel: "Idioma",
      errorPrefix: "Error",
      loading: "Cargando…",
      requiredDate: "Debes indicar la fecha de inicio de la semana.",
      days: ["Lunes", "Martes", "Miércoles", "Jueves", "Viernes", "Sábado", "Domingo"],
    },
  };

  const DAY_KEYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

  const I18N = {
    strings: STRINGS,
    dayKeys: DAY_KEYS,
    lang: localStorage.getItem("lang") || "ca",
    t(key) {
      const dict = STRINGS[this.lang] || STRINGS.ca;
      return dict[key] !== undefined ? dict[key] : key;
    },
    dayLabel(index) {
      return this.t("days")[index] || DAY_KEYS[index];
    },
    setLang(lang) {
      if (!STRINGS[lang]) return;
      this.lang = lang;
      localStorage.setItem("lang", lang);
    },
  };

  window.I18N = I18N;
})();
