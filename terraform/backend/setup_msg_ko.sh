# Korean words of setup.sh's screen, translated from setup_msg_en.sh.

ui_fmt_ko() {
  case "$1" in
    intro) printf '%s' "SharePi 백엔드를 설정합니다. 자세한 내용은 다음 로그에 기록됩니다:" ;;
    step_project) printf '%s' "프로젝트를 확인하는 중" ;;
    step_terraform) printf '%s' "Terraform을 준비하는 중" ;;
    step_apis) printf '%s' "필요한 Google Cloud 서비스를 사용 설정하는 중" ;;
    step_state) printf '%s' "설정 상태를 보관할 위치를 준비하는 중" ;;
    step_admin) printf '%s' "관리자를 확인하는 중" ;;
    step_init) printf '%s' "Terraform을 초기화하는 중" ;;
    step_apply) printf '%s' "백엔드를 만드는 중(몇 분 걸립니다)" ;;
    step_result) printf '%s' "결과를 읽는 중" ;;
    ok) printf '%s' "완료" ;;
    failed) printf '%s' "실패" ;;
    created) printf '%s' "만들었습니다" ;;
    already_there) printf '%s' "이미 있습니다" ;;
    admin_account) printf '%s' "내 Google 계정" ;;
    admin_no_id) printf '%s' "Google 사용자 ID를 읽지 못해 이메일 주소로 확인합니다" ;;
    admin_no_id_note) printf '%s' "      나중에 이 명령을 다시 실행하면 사용자 ID로 확인합니다. 또는 admin_subs를 지정하세요\n      (terraform.tfvars.example 참고)." ;;
    retrying) printf '%s' " (서비스가 아직 준비되지 않았습니다. 60초 후에 다시 시도합니다) " ;;
    what_happened) printf '%s' "무슨 일이 있었나요" ;;
    what_to_do) printf '%s' "해야 할 일" ;;
    last_lines) printf '%s' "이 단계가 마지막으로 출력한 내용:" ;;
    full_log) printf '%s' "전체 로그: %s" ;;
    final_admin) printf '%s' "앱에는 이 Cloud Shell에서 사용한 것과 같은 Google 계정으로 로그인하세요.\n그 계정이 관리자(확인 방식: %s)이며 그룹을 만들고 사람들을 초대할 수 있습니다." ;;
    final_log) printf '%s' "자세한 내용은 다음 로그에 있습니다:" ;;
    final_created) printf '%s' "백엔드가 준비되었습니다. SharePi 앱의 프로필에서 아래 URL을 등록하세요:" ;;
    final_unchanged) printf '%s' "백엔드가 이미 실행 중입니다. SharePi 앱의 프로필에서 아래 URL을 등록하세요:" ;;
    final_updated) printf '%s' "백엔드를 업데이트했습니다. SharePi 앱의 프로필에서 아래 URL을 등록하세요:" ;;
  esac
}

ui_body_ko() {
  local p="${SP_PROJECT:-<PROJECT_ID>}" r="${SP_REGION:-<REGION>}"
  case "$1" in
    billing) cat << EOF
  이 프로젝트에는 결제 계정이 연결되어 있지 않으며, 결제 계정이 없으면 Google Cloud가
  아무것도 만들지 않습니다. 아직 아무것도 만들어지지 않았습니다.

$(ui_t what_to_do)
  1. https://console.cloud.google.com/billing/linkedaccount?project=$p 를 엽니다
  2. "결제 계정 연결"을 누르고 결제 계정을 선택합니다.
     (아직 결제 계정이 없나요? https://console.cloud.google.com/billing 에서
     만든 다음 프로젝트에 연결합니다.)
  또는 이 Cloud Shell에서:
     gcloud billing accounts list
     gcloud billing projects link $p --billing-account=<ACCOUNT_ID>

그런 다음 같은 설정 명령을 다시 실행하세요. 반복해도 안전합니다.
EOF
      ;;
    protected) cat << EOF
  이미 있는 Cloud Run 서비스(예: 이전 버전이 만든 "chamagon-backend")를 다시 만들어야 하는데,
  그 서비스가 삭제로부터 보호되어 있습니다.

$(ui_t what_to_do)
  1. 어떤 서비스인지 확인합니다:
     gcloud run services list --project=$p
  2. 삭제합니다(이전 버전의 서비스 이름은 chamagon-backend입니다):
     gcloud run services delete chamagon-backend --region=$r --project=$p --quiet
     삭제되는 것은 서비스뿐입니다. 사진, 그룹, 멤버는 버킷에 그대로 남습니다.
  3. 같은 설정 명령을 다시 실행합니다.
EOF
      ;;
    terraform) cat << EOF
  이 Cloud Shell에는 Terraform이 설치되어 있지 않고, 자동으로 가져오지도 못했습니다.

$(ui_t what_to_do)
  https://developer.hashicorp.com/terraform/install 의 안내에 따라 설치한 다음,
  같은 설정 명령을 다시 실행하세요.
EOF
      ;;
    not_ready) cat << EOF
  방금 사용 설정한 Google Cloud API가 아직 모든 곳에서 활성화되지 않았습니다.
  보통 몇 분 안에 해결됩니다.

$(ui_t what_to_do)
  2~3분 기다린 뒤 같은 설정 명령을 다시 실행하세요.
EOF
      ;;
    quota) cat << EOF
  Google Cloud가 지금은 이 리전에서 Cloud Run을 시작하는 것을 거부했습니다(프로젝트가 짧은 시간에
  사용하기 시작할 수 있는 리전 수에는 프로젝트별 한도가 있습니다).

$(ui_t what_to_do)
  몇 분 기다린 뒤 같은 설정 명령을 다시 실행하세요. 계속 발생하면 새 프로젝트에서
  다른 지역 코드(두 번째 인수)를 사용해 보세요.
EOF
      ;;
    bucket_taken) cat << EOF
  설정 상태를 보관하는 버킷의 이름("$p-tfstate")을 이미 다른 프로젝트가
  사용하고 있습니다. 버킷 이름은 Google Cloud의 모든 사용자가 함께 씁니다.

$(ui_t what_to_do)
  버킷에 사용된 적이 없는 ID의 프로젝트를 사용하세요. 내 것이 아닌
  버킷을 사용하려고 하지 마세요.
EOF
      ;;
    no_project) cat << EOF
  ID가 "$p"인 프로젝트를 Google Cloud에서 찾을 수 없거나, 이 계정에서 볼 수 없습니다.

$(ui_t what_to_do)
  1. 이 계정이 볼 수 있는 프로젝트를 나열하고 ID(이름이 아님)를 복사합니다:
     gcloud projects list
  2. Cloud Shell이 프로젝트를 만든 계정으로 로그인되어 있는지 확인합니다.
  3. 올바른 프로젝트 ID로 설정 명령을 다시 실행합니다.
EOF
      ;;
    permission) cat << EOF
  이 계정은 프로젝트 "$p"에서 그 작업을 할 권한이 없습니다.

$(ui_t what_to_do)
  1. 프로젝트를 만든 계정(또는 소유자 역할이 있는 계정)을 사용합니다.
     Cloud Shell 창의 오른쪽 위에 표시됩니다.
  2. 프로젝트 ID가 의도한 것인지 확인합니다:
     gcloud projects list
  3. 같은 설정 명령을 다시 실행합니다.
EOF
      ;;
    network) cat << EOF
  네트워크 요청이 실패했습니다.

$(ui_t what_to_do)
  1분 정도 기다린 뒤 같은 설정 명령을 다시 실행하세요.
EOF
      ;;
    *) cat << EOF
  한 단계가 실패했지만, 원인이 이 스크립트가 아는 것이 아닙니다.

$(ui_t what_to_do)
  같은 설정 명령을 다시 실행하세요. 두 번째 시도에서 해결되는 실패가 많습니다. 같은 방식으로
  실패하면 아래에 표시된 로그 파일을 이 명령을 알려 준 사람에게 보내세요. 비밀번호나 토큰은
  포함되어 있지 않습니다.
EOF
      ;;
  esac
}
