-- Ablage des Essensplaners. Wird beim Start angelegt, wenn sie fehlt.
CREATE TABLE IF NOT EXISTS rezepte (
    id            SERIAL PRIMARY KEY,
    titel         TEXT NOT NULL,
    beschreibung  TEXT NOT NULL DEFAULT '',
    portionen     INT  NOT NULL DEFAULT 2,
    dauer_min     INT  NOT NULL DEFAULT 30,
    art           TEXT NOT NULL DEFAULT 'hauptgericht',
    kost          TEXT NOT NULL DEFAULT 'fleisch',
    aufwand       TEXT NOT NULL DEFAULT 'mittel',
    saison        TEXT[] NOT NULL DEFAULT '{}',
    etiketten     TEXT[] NOT NULL DEFAULT '{}',
    zutaten       JSONB NOT NULL DEFAULT '[]',
    schritte      JSONB NOT NULL DEFAULT '[]',
    hinweise      TEXT NOT NULL DEFAULT '',
    quelle        TEXT NOT NULL DEFAULT '',
    bewertung     INT  NOT NULL DEFAULT 0,
    zuletzt_gekocht DATE,
    angelegt      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plaene (
    id          SERIAL PRIMARY KEY,
    woche       DATE NOT NULL UNIQUE,
    personen    INT  NOT NULL DEFAULT 2,
    modell      TEXT NOT NULL DEFAULT '',
    begruendung TEXT NOT NULL DEFAULT '',
    erzeugt     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plan_tage (
    id        SERIAL PRIMARY KEY,
    plan_id   INT NOT NULL REFERENCES plaene(id) ON DELETE CASCADE,
    tag       DATE NOT NULL,
    rezept_id INT REFERENCES rezepte(id) ON DELETE SET NULL,
    hinweis   TEXT NOT NULL DEFAULT '',
    gekocht   BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (plan_id, tag)
);

CREATE TABLE IF NOT EXISTS einkauf (
    id        SERIAL PRIMARY KEY,
    plan_id   INT NOT NULL REFERENCES plaene(id) ON DELETE CASCADE,
    name      TEXT NOT NULL,
    menge     NUMERIC NOT NULL DEFAULT 0,
    einheit   TEXT NOT NULL DEFAULT '',
    kategorie TEXT NOT NULL DEFAULT 'Sonstiges',
    abgehakt  BOOLEAN NOT NULL DEFAULT false,
    zusatz    BOOLEAN NOT NULL DEFAULT false
);

-- Was immer im Haus ist, landet nicht auf der Einkaufsliste.
CREATE TABLE IF NOT EXISTS vorrat (
    id   SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS regeln (
    schluessel TEXT PRIMARY KEY,
    wert       TEXT NOT NULL
);

-- Eigene Lebensmittel neben dem BLS, Werte je 100 g als {Naehrstoffcode: Wert}.
CREATE TABLE IF NOT EXISTS lebensmittel (
    id        SERIAL PRIMARY KEY,
    name      TEXT NOT NULL,
    stueck_g  NUMERIC NOT NULL DEFAULT 0,
    werte     JSONB NOT NULL DEFAULT '{}',
    angelegt  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Von Hand festgelegt: welche Zutat welches Lebensmittel ist. Gilt fuer alle
-- Rezepte; der Schluessel ist der gefaltete Zutatenname.
CREATE TABLE IF NOT EXISTS zuordnung (
    zutat        TEXT PRIMARY KEY,
    lebensmittel TEXT NOT NULL,
    stueck_g     NUMERIC NOT NULL DEFAULT 0
);
