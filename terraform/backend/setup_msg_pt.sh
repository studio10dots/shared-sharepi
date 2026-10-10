# Portuguese words of setup.sh's screen, translated from setup_msg_en.sh.

ui_fmt_pt() {
  case "$1" in
    intro) printf '%s' "Configurando seu backend do SharePi. Os detalhes são gravados em:" ;;
    step_project) printf '%s' "Verificando seu projeto" ;;
    step_terraform) printf '%s' "Preparando o Terraform" ;;
    step_apis) printf '%s' "Ativando os serviços do Google Cloud que ele usa" ;;
    step_state) printf '%s' "Preparando o local onde fica o estado da configuração" ;;
    step_admin) printf '%s' "Descobrindo quem é o administrador" ;;
    step_init) printf '%s' "Inicializando o Terraform" ;;
    step_apply) printf '%s' "Criando seu backend (leva alguns minutos)" ;;
    step_result) printf '%s' "Lendo o resultado" ;;
    ok) printf '%s' "ok" ;;
    failed) printf '%s' "falhou" ;;
    created) printf '%s' "criado" ;;
    already_there) printf '%s' "já existe" ;;
    admin_account) printf '%s' "sua conta do Google" ;;
    admin_no_id) printf '%s' "não foi possível ler seu ID de usuário do Google; seu e-mail será usado" ;;
    admin_no_id_note) printf '%s' "      Execute este comando novamente mais tarde para fixá-lo ao seu ID de usuário, ou defina admin_subs\n      (veja terraform.tfvars.example)." ;;
    retrying) printf '%s' " (um serviço ainda não está pronto; nova tentativa em 60 segundos) " ;;
    what_happened) printf '%s' "O que aconteceu" ;;
    what_to_do) printf '%s' "O que fazer" ;;
    last_lines) printf '%s' "As últimas linhas que a etapa mostrou:" ;;
    full_log) printf '%s' "Registro completo: %s" ;;
    final_admin) printf '%s' "Entre no app com a mesma conta do Google que você usou neste Cloud Shell:\nessa conta é a administradora (reconhecida por: %s) e pode criar grupos e convidar pessoas." ;;
    final_log) printf '%s' "Os detalhes estão neste registro:" ;;
    final_created) printf '%s' "Seu backend está pronto. No app SharePi, vá em Perfil e registre a URL abaixo:" ;;
    final_unchanged) printf '%s' "Seu backend já está em execução. No app SharePi, vá em Perfil e registre a URL abaixo:" ;;
    final_updated) printf '%s' "Seu backend foi atualizado. No app SharePi, vá em Perfil e registre a URL abaixo:" ;;
  esac
}

ui_body_pt() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  Este projeto não tem nenhuma conta de faturamento vinculada, e o Google Cloud não criará
  nada sem uma. Nada foi criado ainda.

$(ui_t what_to_do)
  1. Abra https://console.cloud.google.com/billing/linkedaccount?project=$p
  2. Clique em "Vincular uma conta de faturamento" e escolha sua conta de faturamento.
     (Ainda não tem uma? Crie uma em https://console.cloud.google.com/billing
     e vincule-a ao projeto.)
  Ou aqui, no Cloud Shell:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

Depois, execute o mesmo comando de configuração novamente. É seguro repetir.
EOF
      ;;
    protected) cat << EOF
  A configuração precisa substituir um serviço do Cloud Run que já existe (por exemplo,
  o que uma versão anterior criou, "chamagon-backend"), e esse serviço está protegido
  contra exclusão.

$(ui_t what_to_do)
  1. Veja de qual serviço se trata:
     gcloud run services list --project=$p
  2. Exclua-o (o anterior se chama chamagon-backend):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     Apenas o serviço é excluído. Suas fotos, grupos e membros continuam no bucket.
  3. Execute o mesmo comando de configuração novamente.
EOF
      ;;
    terraform) cat << EOF
  O Terraform não está instalado neste Cloud Shell e não pôde ser obtido automaticamente.

$(ui_t what_to_do)
  Instale-o seguindo https://developer.hashicorp.com/terraform/install
  e execute o mesmo comando de configuração novamente.
EOF
      ;;
    not_ready) cat << EOF
  Uma API do Google Cloud ativada há pouco ainda não está ativa em todos os lugares.
  Isso costuma se resolver em alguns minutos.

$(ui_t what_to_do)
  Espere dois ou três minutos e execute o mesmo comando de configuração novamente.
EOF
      ;;
    quota) cat << EOF
  O Google Cloud se recusou, por enquanto, a iniciar o Cloud Run nesta região (há um limite
  por projeto de quantas regiões ele pode começar a usar em pouco tempo).

$(ui_t what_to_do)
  Espere alguns minutos e execute o mesmo comando de configuração novamente. Se continuar
  acontecendo, tente outro código de área (o segundo argumento) em um projeto novo.
EOF
      ;;
    bucket_taken) cat << EOF
  O nome do bucket do estado da configuração ("$p-tfstate") já está em uso por
  outro projeto. Os nomes de bucket são compartilhados por todos no Google Cloud.

$(ui_t what_to_do)
  Use um projeto cujo ID nunca tenha sido usado para um bucket. Não tente
  reutilizar um bucket que não é seu.
EOF
      ;;
    no_project) cat << EOF
  O Google Cloud não encontra nenhum projeto com o ID "$p", ou esta conta não consegue vê-lo.

$(ui_t what_to_do)
  1. Liste os projetos que esta conta enxerga e copie o ID (não o nome):
     gcloud projects list
  2. Confirme que o Cloud Shell está conectado com a conta que criou o projeto.
  3. Execute o comando de configuração novamente com o ID de projeto correto.
EOF
      ;;
    permission) cat << EOF
  Esta conta não tem permissão para fazer isso no projeto "$p".

$(ui_t what_to_do)
  1. Use a conta que criou o projeto (ou que tem o papel de Proprietário nele).
     Ela aparece no canto superior direito da janela do Cloud Shell.
  2. Confira se o ID do projeto é o que você queria:
     gcloud projects list
  3. Execute o mesmo comando de configuração novamente.
EOF
      ;;
    network) cat << EOF
  Uma solicitação de rede falhou.

$(ui_t what_to_do)
  Espere um minuto e execute o mesmo comando de configuração novamente.
EOF
      ;;
    *) cat << EOF
  Uma etapa falhou, e a causa não é uma que este script conheça.

$(ui_t what_to_do)
  Execute o mesmo comando de configuração novamente; muitas falhas somem na segunda
  tentativa. Se falhar do mesmo jeito, envie o arquivo de registro indicado abaixo para a
  pessoa que lhe passou este comando. Ele não contém senhas nem tokens.
EOF
      ;;
  esac
}
