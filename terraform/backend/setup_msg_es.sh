# Spanish words of setup.sh's screen, translated from setup_msg_en.sh.

ui_fmt_es() {
  case "$1" in
    intro) printf '%s' "Configurando tu backend de SharePi. Los detalles se escriben en:" ;;
    step_project) printf '%s' "Comprobando tu proyecto" ;;
    step_terraform) printf '%s' "Preparando Terraform" ;;
    step_apis) printf '%s' "Activando los servicios de Google Cloud que usa" ;;
    step_state) printf '%s' "Preparando el lugar donde se guarda el estado de la configuración" ;;
    step_admin) printf '%s' "Averiguando quién es el administrador" ;;
    step_init) printf '%s' "Inicializando Terraform" ;;
    step_apply) printf '%s' "Creando tu backend (tarda unos minutos)" ;;
    step_result) printf '%s' "Leyendo el resultado" ;;
    ok) printf '%s' "listo" ;;
    failed) printf '%s' "error" ;;
    created) printf '%s' "creado" ;;
    already_there) printf '%s' "ya existe" ;;
    admin_account) printf '%s' "tu cuenta de Google" ;;
    admin_no_id) printf '%s' "no se pudo leer tu ID de usuario de Google; se usará tu correo electrónico" ;;
    admin_no_id_note) printf '%s' "      Vuelve a ejecutar este comando más tarde para fijarlo a tu ID de usuario, o define admin_subs\n      (consulta terraform.tfvars.example)." ;;
    retrying) printf '%s' " (un servicio aún no está listo; se reintentará en 60 segundos) " ;;
    what_happened) printf '%s' "Qué ha pasado" ;;
    what_to_do) printf '%s' "Qué hacer" ;;
    last_lines) printf '%s' "Las últimas líneas que mostró el paso:" ;;
    full_log) printf '%s' "Registro completo: %s" ;;
    final_admin) printf '%s' "Inicia sesión en la app con la misma cuenta de Google que usaste en este Cloud Shell:\nesa cuenta es la administradora (reconocida por: %s) y puede crear grupos e invitar a personas." ;;
    final_log) printf '%s' "Los detalles están en este registro:" ;;
    final_created) printf '%s' "Tu backend está listo. En la app SharePi, ve a Perfil y registra la URL de abajo:" ;;
    final_unchanged) printf '%s' "Tu backend ya está en marcha. En la app SharePi, ve a Perfil y registra la URL de abajo:" ;;
    final_updated) printf '%s' "Tu backend se ha actualizado. En la app SharePi, ve a Perfil y registra la URL de abajo:" ;;
  esac
}

ui_body_es() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  Este proyecto no tiene ninguna cuenta de facturación vinculada, y Google Cloud no creará
  nada sin una. Todavía no se ha creado nada.

$(ui_t what_to_do)
  1. Abre https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Haz clic en "Vincular una cuenta de facturación" y elige tu cuenta de facturación.
     (¿Aún no tienes una? Crea una en https://console.cloud.google.com/billing
     y vincúlala al proyecto.)
  O aquí, en Cloud Shell:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Después, vuelve a ejecutar el mismo comando de configuración. Es seguro repetirlo.
EOF
      ;;
    protected) cat << EOF
  La configuración tiene que reemplazar un servicio de Cloud Run que ya existe (por ejemplo,
  el que creó una versión anterior, "chamagon-backend"), y ese servicio está protegido
  contra la eliminación.

$(ui_t what_to_do)
  1. Mira de qué servicio se trata:
     gcloud run services list --project=$p
  2. Elimínalo (el anterior se llama chamagon-backend):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     Solo se elimina el servicio. Tus fotos, grupos y miembros siguen en el bucket.
  3. Vuelve a ejecutar el mismo comando de configuración.
EOF
      ;;
    terraform) cat << EOF
  Terraform no está instalado en este Cloud Shell y no se pudo descargar automáticamente.

$(ui_t what_to_do)
  Instálalo siguiendo https://developer.hashicorp.com/terraform/install y
  vuelve a ejecutar el mismo comando de configuración.
EOF
      ;;
    not_ready) cat << EOF
  Una API de Google Cloud que se activó hace un momento todavía no está activa en todas partes.
  Suele resolverse en unos minutos.

$(ui_t what_to_do)
  Espera dos o tres minutos y vuelve a ejecutar el mismo comando de configuración.
EOF
      ;;
    quota) cat << EOF
  Google Cloud se ha negado, por ahora, a iniciar Cloud Run en esta región (hay un límite
  por proyecto sobre cuántas regiones puede empezar a usar en poco tiempo).

$(ui_t what_to_do)
  Espera unos minutos y vuelve a ejecutar el mismo comando de configuración. Si sigue
  ocurriendo, prueba con otro código de zona (el segundo argumento) en un proyecto nuevo.
EOF
      ;;
    bucket_taken) cat << EOF
  El nombre del bucket del estado de la configuración ("$p-tfstate") ya lo usa
  otro proyecto. Los nombres de bucket son compartidos por todos en Google Cloud.

$(ui_t what_to_do)
  Usa un proyecto cuyo ID no se haya usado antes para un bucket. No intentes
  reutilizar un bucket que no es tuyo.
EOF
      ;;
    no_project) cat << EOF
  Google Cloud no encuentra ningún proyecto con el ID "$p", o esta cuenta no puede verlo.

$(ui_t what_to_do)
  1. Lista los proyectos que ve esta cuenta y copia el ID (no el nombre):
     gcloud projects list
  2. Comprueba que Cloud Shell ha iniciado sesión con la cuenta que creó el proyecto.
  3. Vuelve a ejecutar el comando de configuración con el ID de proyecto correcto.
EOF
      ;;
    permission) cat << EOF
  Esta cuenta no tiene permiso para hacer eso en el proyecto "$p".

$(ui_t what_to_do)
  1. Usa la cuenta que creó el proyecto (o que tiene el rol de Propietario en él).
     Aparece arriba a la derecha en la ventana de Cloud Shell.
  2. Comprueba que el ID del proyecto es el que querías:
     gcloud projects list
  3. Vuelve a ejecutar el mismo comando de configuración.
EOF
      ;;
    network) cat << EOF
  Ha fallado una solicitud de red.

$(ui_t what_to_do)
  Espera un minuto y vuelve a ejecutar el mismo comando de configuración.
EOF
      ;;
    *) cat << EOF
  Un paso ha fallado y la causa no es una que este script conozca.

$(ui_t what_to_do)
  Vuelve a ejecutar el mismo comando de configuración; muchos fallos se resuelven al
  segundo intento. Si falla igual, envía el archivo de registro indicado abajo a la
  persona que te dio este comando. No contiene contraseñas ni tokens.
EOF
      ;;
  esac
}
