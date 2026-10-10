# French words of setup.sh's screen, translated from setup_msg_en.sh.

ui_fmt_fr() {
  case "$1" in
    intro) printf '%s' "Configuration de votre backend SharePi. Les détails sont écrits dans :" ;;
    step_project) printf '%s' "Vérification de votre projet" ;;
    step_terraform) printf '%s' "Préparation de Terraform" ;;
    step_apis) printf '%s' "Activation des services Google Cloud utilisés" ;;
    step_state) printf '%s' "Préparation de l'emplacement qui conserve l'état de la configuration" ;;
    step_admin) printf '%s' "Recherche de l'administrateur" ;;
    step_init) printf '%s' "Initialisation de Terraform" ;;
    step_apply) printf '%s' "Création de votre backend (quelques minutes)" ;;
    step_result) printf '%s' "Lecture du résultat" ;;
    ok) printf '%s' "ok" ;;
    failed) printf '%s' "échec" ;;
    created) printf '%s' "créé" ;;
    already_there) printf '%s' "déjà présent" ;;
    admin_account) printf '%s' "votre compte Google" ;;
    admin_no_id) printf '%s' "impossible de lire votre identifiant d'utilisateur Google ; votre adresse e-mail sera utilisée" ;;
    admin_no_id_note) printf '%s' "      Relancez cette commande plus tard pour la fixer à votre identifiant, ou définissez admin_subs\n      (voir terraform.tfvars.example)." ;;
    retrying) printf '%s' " (un service n'est pas encore prêt ; nouvelle tentative dans 60 secondes) " ;;
    what_happened) printf '%s' "Ce qui s'est passé" ;;
    what_to_do) printf '%s' "Ce qu'il faut faire" ;;
    last_lines) printf '%s' "Les dernières lignes affichées par l'étape :" ;;
    full_log) printf '%s' "Journal complet : %s" ;;
    final_admin) printf '%s' "Connectez-vous à l'application avec le même compte Google que dans ce Cloud Shell :\nce compte est l'administrateur (reconnu par : %s) et peut créer des groupes et inviter des personnes." ;;
    final_log) printf '%s' "Les détails sont dans ce journal :" ;;
    final_created) printf '%s' "Votre backend est prêt. Dans l'application SharePi, allez dans Profil et enregistrez l'URL ci-dessous :" ;;
    final_unchanged) printf '%s' "Votre backend est déjà en cours d'exécution. Dans l'application SharePi, allez dans Profil et enregistrez l'URL ci-dessous :" ;;
    final_updated) printf '%s' "Votre backend a été mis à jour. Dans l'application SharePi, allez dans Profil et enregistrez l'URL ci-dessous :" ;;
  esac
}

ui_body_fr() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  Aucun compte de facturation n'est associé à ce projet, et Google Cloud ne créera
  rien sans cela. Rien n'a encore été créé.

$(ui_t what_to_do)
  1. Ouvrez https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Cliquez sur « Associer un compte de facturation » et choisissez votre compte de facturation.
     (Pas encore de compte de facturation ? Créez-en un sur https://console.cloud.google.com/billing
     puis associez-le au projet.)
  Ou ici, dans Cloud Shell :
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Relancez ensuite la même commande de configuration. Vous pouvez la répéter sans risque.
EOF
      ;;
    protected) cat << EOF
  La configuration doit remplacer un service Cloud Run qui existe déjà (par exemple
  celui qu'une ancienne version a créé, « chamagon-backend »), et ce service est
  protégé contre la suppression.

$(ui_t what_to_do)
  1. Voyez de quel service il s'agit :
     gcloud run services list --project=$p
  2. Supprimez-le (l'ancien s'appelle chamagon-backend) :
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     Seul le service est supprimé. Vos photos, groupes et membres restent dans le bucket.
  3. Relancez la même commande de configuration.
EOF
      ;;
    terraform) cat << EOF
  Terraform n'est pas installé dans ce Cloud Shell et n'a pas pu être récupéré automatiquement.

$(ui_t what_to_do)
  Installez-le en suivant https://developer.hashicorp.com/terraform/install,
  puis relancez la même commande de configuration.
EOF
      ;;
    not_ready) cat << EOF
  Une API Google Cloud activée à l'instant n'est pas encore active partout.
  Cela se règle généralement en quelques minutes.

$(ui_t what_to_do)
  Attendez deux ou trois minutes, puis relancez la même commande de configuration.
EOF
      ;;
    quota) cat << EOF
  Google Cloud a refusé, pour l'instant, de démarrer Cloud Run dans cette région (il existe une
  limite par projet sur le nombre de régions qu'un projet peut commencer à utiliser en peu de temps).

$(ui_t what_to_do)
  Attendez quelques minutes et relancez la même commande de configuration. Si cela se
  reproduit, essayez un autre code de zone (le deuxième argument) dans un nouveau projet.
EOF
      ;;
    bucket_taken) cat << EOF
  Le nom du bucket de l'état de la configuration (« $p-tfstate ») est déjà pris par
  un autre projet. Les noms de bucket sont partagés par tous sur Google Cloud.

$(ui_t what_to_do)
  Utilisez un projet dont l'ID n'a jamais servi pour un bucket. N'essayez pas de
  réutiliser un bucket qui ne vous appartient pas.
EOF
      ;;
    no_project) cat << EOF
  Google Cloud ne trouve aucun projet avec l'ID « $p », ou ce compte ne peut pas le voir.

$(ui_t what_to_do)
  1. Listez les projets que ce compte peut voir et copiez l'ID (pas le nom) :
     gcloud projects list
  2. Vérifiez que Cloud Shell est connecté avec le compte qui a créé le projet.
  3. Relancez la commande de configuration avec le bon ID de projet.
EOF
      ;;
    permission) cat << EOF
  Ce compte n'est pas autorisé à faire cela dans le projet « $p ».

$(ui_t what_to_do)
  1. Utilisez le compte qui a créé le projet (ou qui en est Propriétaire).
     Il est affiché en haut à droite de la fenêtre Cloud Shell.
  2. Vérifiez que l'ID du projet est bien celui voulu :
     gcloud projects list
  3. Relancez la même commande de configuration.
EOF
      ;;
    network) cat << EOF
  Une requête réseau a échoué.

$(ui_t what_to_do)
  Attendez une minute et relancez la même commande de configuration.
EOF
      ;;
    *) cat << EOF
  Une étape a échoué, et la cause n'est pas de celles que ce script connaît.

$(ui_t what_to_do)
  Relancez la même commande de configuration ; de nombreux échecs disparaissent au
  deuxième essai. S'il échoue de la même façon, envoyez le fichier journal indiqué
  ci-dessous à la personne qui vous a donné cette commande. Il ne contient ni mots de passe ni jetons.
EOF
      ;;
  esac
}
