// The video's captions in bokmål and nynorsk, for its subtitle tracks (site/media/demo.<lang>.vtt): the video itself
// is in English, with the English captions in the picture. Keyed by the English text video.spec.js shows; a caption
// with no translation here fails the run.
const TEXTS = {
  'Sermon Studio': ['Sermon Studio', 'Sermon Studio'],
  'From the recording of a service to sermon files': ['Fra opptaket av gudstjenesten til prekenfiler', 'Frå opptaket av gudstenesta til preikefiler'],
  '1. Send the recording': ['1. Send opptaket', '1. Send opptaket'],
  'No login: the code from the church is enough': ['Ingen innlogging: koden fra menigheten er nok', 'Inga innlogging: koden frå kyrkjelyden er nok'],
  '2. It is in the studio at once': ['2. Det er i studioet med en gang', '2. Det er i studioet med ein gong'],
  'Filed under the date in its name': ['Lagt under datoen i filnavnet', 'Lagt under datoen i filnamnet'],
  '3. Mark the sermon': ['3. Merk prekenen', '3. Merk preika'],
  'Drag across it on the waveform': ['Dra over den på bølgeformen', 'Dra over ho på bølgjeforma'],
  'Remove the guess, then drag across the sermon': ['Fjern forslaget, og dra over prekenen', 'Fjern framlegget, og dra over preika'],
  'Hear it as it will be published': ['Hør den slik den blir publisert', 'Høyr ho slik ho vert publisert'],
  '4. Cut out a cough': ['4. Klipp ut et host', '4. Klipp ut eit host'],
  'Hold shift and drag across it': ['Hold shift og dra over det', 'Hald shift og dra over det'],
  'A label says what it was': ['En merkelapp sier hva det var', 'Ein merkelapp seier kva det var'],
  'Hear join plays across the cut': ['«Hør skjøten» spiller over klippet', '«Høyr skøyten» spelar over klippet'],
  '5. Choose how it sounds': ['5. Velg hvordan det skal låte', '5. Vel korleis det skal låte'],
  'In the Processing step: stronger leveling': ['I steget Behandling: sterkere utjevning', 'I steget Handsaming: sterkare utjamning'],
  '6. Add an intro': ['6. Legg til en intro', '6. Legg til ein intro'],
  'Upload for this sermon only': ['Last opp for bare denne prekenen', 'Last opp for berre denne preika'],
  'Hear how it meets the sermon': ['Hør hvordan den møter prekenen', 'Høyr korleis han møter preika'],
  '7. Save: make the FLAC and the MP3': ['7. Lagre: lag FLAC og MP3', '7. Lagre: lag FLAC og MP3'],
  'The run\'s steps, one after another': ['Stegene i kjøringen, ett etter ett', 'Stega i køyringa, eitt etter eitt'],
  '8. The files are ready': ['8. Filene er klare', '8. Filene er klare'],
  'The transcript is made in the background': ['Transkripsjonen lages i bakgrunnen', 'Transkripsjonen vert laga i bakgrunnen'],
  'With the intro, or the sermon only': ['Med introen, eller bare prekenen', 'Med introen, eller berre preika'],
  'Listen before you share': ['Lytt før du deler', 'Lytt før du deler'],
  '9. Share it': ['9. Del den', '9. Del ho'],
  'For those who were not there': ['Til dem som ikke var der', 'Til dei som ikkje var der'],
  'A code, a link and a QR code': ['En kode, en lenke og en QR-kode', 'Ein kode, ei lenkje og ein QR-kode'],
  '10. Anyone with the code downloads it': ['10. Alle med koden kan laste den ned', '10. Alle med koden kan laste ho ned'],
  'No login here either': ['Ingen innlogging her heller', 'Inga innlogging her heller'],
  'Try it yourself: demo.sermon-studio.app': ['Prøv selv: demo.sermon-studio.app', 'Prøv sjølv: demo.sermon-studio.app'],
  'Free for churches: write to contact@sermon-studio.app': ['Gratis for menigheter: skriv til contact@sermon-studio.app', 'Gratis for kyrkjelydar: skriv til contact@sermon-studio.app'],
};
const LANGS = ['nb', 'nn'];

const stamp = (s) => {
  const ms = Math.max(0, Math.round(s * 1000));
  const two = (n) => String(n).padStart(2, '0');
  return `${two(Math.floor(ms / 3600000))}:${two(Math.floor(ms / 60000) % 60)}:${two(Math.floor(ms / 1000) % 60)}.${String(ms % 1000).padStart(3, '0')}`;
};

// subtitles gives each language's WebVTT from the captions as shown: cues is [seconds into the video, title, line]
// (an empty title clears the caption), end the video's length.
export function subtitles(cues, end) {
  return Object.fromEntries(LANGS.map((lang, i) => {
    const say = (text) => {
      if (!(text in TEXTS)) throw new Error(`subtitles.js has no translation of "${text}"`);
      return TEXTS[text][i];
    };
    let vtt = 'WEBVTT\n';
    cues.forEach(([from, title, line], n) => {
      if (!title) return;
      const to = n + 1 < cues.length ? cues[n + 1][0] : end;
      if (to - from < 0.2) return;
      vtt += `\n${stamp(from)} --> ${stamp(to)}\n${say(title)}${line ? '\n' + say(line) : ''}\n`;
    });
    return [lang, vtt];
  }));
}
