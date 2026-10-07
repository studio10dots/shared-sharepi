# German words of setup.sh's screen, translated from setup_msg_en.sh.

ui_fmt_de() {
  case "$1" in
    intro) printf '%s' "SharePi-Backend wird eingerichtet. Die Details werden hier gespeichert:" ;;
    step_project) printf '%s' "Projekt wird geprüft" ;;
    step_terraform) printf '%s' "Terraform wird vorbereitet" ;;
    step_apis) printf '%s' "Benötigte Google-Cloud-Dienste werden aktiviert" ;;
    step_state) printf '%s' "Speicherort für den Einrichtungsstatus wird vorbereitet" ;;
    step_admin) printf '%s' "Administrator wird ermittelt" ;;
    step_init) printf '%s' "Terraform wird initialisiert" ;;
    step_apply) printf '%s' "Backend wird erstellt (dauert einige Minuten)" ;;
    step_result) printf '%s' "Ergebnis wird gelesen" ;;
    ok) printf '%s' "fertig" ;;
    failed) printf '%s' "fehlgeschlagen" ;;
    created) printf '%s' "erstellt" ;;
    already_there) printf '%s' "bereits vorhanden" ;;
    admin_account) printf '%s' "dein Google-Konto" ;;
    admin_no_id) printf '%s' "Deine Google-Nutzer-ID konnte nicht gelesen werden; es wird deine E-Mail-Adresse verwendet" ;;
    admin_no_id_note) printf '%s' "      Führe diesen Befehl später erneut aus, um sie an deine Nutzer-ID zu binden, oder setze admin_subs\n      (siehe terraform.tfvars.example)." ;;
    retrying) printf '%s' " (ein Dienst ist noch nicht bereit; neuer Versuch in 60 Sekunden) " ;;
    what_happened) printf '%s' "Was passiert ist" ;;
    what_to_do) printf '%s' "Was zu tun ist" ;;
    last_lines) printf '%s' "Die letzten Zeilen der Ausgabe des Schritts:" ;;
    full_log) printf '%s' "Vollständiges Protokoll: %s" ;;
    final_admin) printf '%s' "Melde dich in der App mit demselben Google-Konto an, das du in diesem Cloud Shell verwendet hast:\ndieses Konto ist der Administrator (erkannt über: %s) und kann Gruppen erstellen und Personen einladen." ;;
    final_log) printf '%s' "Die Details stehen in diesem Protokoll:" ;;
    final_created) printf '%s' "Dein Backend ist bereit. Öffne in der SharePi-App das Profil und registriere die folgende URL:" ;;
    final_unchanged) printf '%s' "Dein Backend läuft bereits. Öffne in der SharePi-App das Profil und registriere die folgende URL:" ;;
    final_updated) printf '%s' "Dein Backend wurde aktualisiert. Öffne in der SharePi-App das Profil und registriere die folgende URL:" ;;
  esac
}

ui_body_de() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  Diesem Projekt ist kein Rechnungskonto zugeordnet, und Google Cloud legt ohne eines
  nichts an. Es wurde noch nichts erstellt.

$(ui_t what_to_do)
  1. Öffne https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Klicke auf „Rechnungskonto verknüpfen“ und wähle dein Rechnungskonto aus.
     (Noch kein Rechnungskonto? Lege unter https://console.cloud.google.com/billing
     eines an und verknüpfe es mit dem Projekt.)
  Oder hier, in Cloud Shell:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Führe danach denselben Einrichtungsbefehl erneut aus. Er kann gefahrlos wiederholt werden.
EOF
      ;;
    protected) cat << EOF
  Die Einrichtung muss einen bereits vorhandenen Cloud-Run-Dienst ersetzen (zum Beispiel
  den, den eine ältere Version angelegt hat, „chamagon-backend“), und dieser Dienst ist
  vor dem Löschen geschützt.

$(ui_t what_to_do)
  1. Sieh nach, um welchen Dienst es sich handelt:
     gcloud run services list --project=$p
  2. Lösche ihn (der ältere heißt chamagon-backend):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     Es wird nur der Dienst gelöscht. Deine Fotos, Gruppen und Mitglieder bleiben im Bucket.
  3. Führe denselben Einrichtungsbefehl erneut aus.
EOF
      ;;
    terraform) cat << EOF
  Terraform ist in diesem Cloud Shell nicht installiert und konnte nicht automatisch geholt werden.

$(ui_t what_to_do)
  Installiere es nach der Anleitung unter https://developer.hashicorp.com/terraform/install
  und führe dann denselben Einrichtungsbefehl erneut aus.
EOF
      ;;
    not_ready) cat << EOF
  Eine Google-Cloud-API, die gerade erst aktiviert wurde, ist noch nicht überall aktiv.
  Das löst sich meist innerhalb weniger Minuten.

$(ui_t what_to_do)
  Warte zwei bis drei Minuten und führe dann denselben Einrichtungsbefehl erneut aus.
EOF
      ;;
    quota) cat << EOF
  Google Cloud hat es vorerst abgelehnt, Cloud Run in dieser Region zu starten (es gibt pro Projekt
  eine Grenze, wie viele Regionen es in kurzer Zeit in Betrieb nehmen darf).

$(ui_t what_to_do)
  Warte einige Minuten und führe denselben Einrichtungsbefehl erneut aus. Tritt es weiterhin auf,
  probiere in einem neuen Projekt einen anderen Gebietscode (das zweite Argument).
EOF
      ;;
    bucket_taken) cat << EOF
  Der Name des Buckets für den Einrichtungsstatus („$p-tfstate“) ist bereits von
  einem anderen Projekt belegt. Bucket-Namen teilen sich alle in Google Cloud.

$(ui_t what_to_do)
  Verwende ein Projekt, dessen ID noch nie für einen Bucket verwendet wurde. Versuche nicht,
  einen Bucket zu verwenden, der dir nicht gehört.
EOF
      ;;
    no_project) cat << EOF
  Google Cloud findet kein Projekt mit der ID „$p“, oder dieses Konto kann es nicht sehen.

$(ui_t what_to_do)
  1. Liste die Projekte auf, die dieses Konto sieht, und kopiere die ID (nicht den Namen):
     gcloud projects list
  2. Prüfe, ob Cloud Shell mit dem Konto angemeldet ist, das das Projekt erstellt hat.
  3. Führe den Einrichtungsbefehl mit der richtigen Projekt-ID erneut aus.
EOF
      ;;
    permission) cat << EOF
  Dieses Konto darf das im Projekt „$p“ nicht tun.

$(ui_t what_to_do)
  1. Verwende das Konto, das das Projekt erstellt hat (oder dort die Rolle Inhaber hat).
     Es wird oben rechts im Cloud-Shell-Fenster angezeigt.
  2. Prüfe, ob die Projekt-ID die gemeinte ist:
     gcloud projects list
  3. Führe denselben Einrichtungsbefehl erneut aus.
EOF
      ;;
    network) cat << EOF
  Eine Netzwerkanfrage ist fehlgeschlagen.

$(ui_t what_to_do)
  Warte eine Minute und führe denselben Einrichtungsbefehl erneut aus.
EOF
      ;;
    *) cat << EOF
  Ein Schritt ist fehlgeschlagen, und die Ursache gehört nicht zu denen, die dieses Skript kennt.

$(ui_t what_to_do)
  Führe denselben Einrichtungsbefehl erneut aus; viele Fehler verschwinden beim zweiten Versuch.
  Schlägt er genauso fehl, sende die unten genannte Protokolldatei an die Person, die dir diesen
  Befehl gegeben hat. Sie enthält keine Passwörter oder Tokens.
EOF
      ;;
  esac
}
