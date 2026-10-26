// Uses the Jenkins/105 conventions already used by ja-alarm-mqtt-kafka-bridge and defenseAI.
// Publishing and the dev 105 systemd upgrade are opt-in; branch/PR builds verify only.
def buildPackages(boolean publish) {
    ['amd64', 'arm64'].each { arch ->
        withEnv(["TARGET_ARCH=${arch}", "PUSH_IMAGE=${publish ? '1' : '0'}"]) {
            sh 'bash ci-package.sh'
        }
    }
}

pipeline {
    agent none
    options {
        skipDefaultCheckout(true)
        disableConcurrentBuilds()
        timestamps()
        timeout(time: 60, unit: 'MINUTES')
        buildDiscarder(logRotator(numToKeepStr: '20', artifactNumToKeepStr: '10'))
    }
    parameters {
        string(name: 'SCM_AGENT_LABEL', defaultValue: 'built-in', description: '114 源码检出节点，避免 105 直连 GitHub 不稳定')
        string(name: 'BUILD_AGENT_LABEL', defaultValue: 'ja-pipeline-builder-105', description: 'Linux amd64 节点；需要 Docker、Bash、Python 3.8+，不需要宿主机 Go/Node')
        booleanParam(name: 'PUBLISH_IMAGE', defaultValue: false, description: '验证后推送镜像；仅 master/dev/test/release，禁止 PR')
        booleanParam(name: 'DEPLOY_DEV', defaultValue: false, description: '仅人工触发 dev，升级 105 独立开发实例；必须先初始化服务及 /etc/ja-rttys-dev/ci-deploy.json')
    }
    stages {
        stage('Prepare selected source') {
            agent { label params.SCM_AGENT_LABEL }
            steps {
                script {
                    def revision
                    retry(3) { revision = checkout scm }
                    env.PIPELINE_BRANCH = (env.BRANCH_NAME ?: revision.GIT_BRANCH ?: '').replaceFirst('^origin/', '')
                    if ((params.PUBLISH_IMAGE || params.DEPLOY_DEV) && (env.CHANGE_ID || !(env.PIPELINE_BRANCH in ['master', 'dev', 'test', 'release']))) {
                        error('当前分支/PR 只执行验证和出包，不允许发布或部署')
                    }
                    if (params.DEPLOY_DEV && (env.PIPELINE_BRANCH != 'dev' || currentBuild.getBuildCauses('hudson.model.Cause$UserIdCause').isEmpty())) {
                        error('105 开发环境部署仅允许人工触发的 dev 构建')
                    }
                    env.SOURCE_COMMIT = sh(script: 'git rev-parse HEAD', returnStdout: true).trim()
                    def baseVersion = sh(script: "sed -n 's/^const RttysVersion = \"\\(.*\\)\"/\\1/p' cmd/rttys/main.go", returnStdout: true).trim()
                    if (!(baseVersion ==~ /[A-Za-z0-9_.-]+/)) { error('无效服务版本') }
                    def date = sh(script: 'date -u +%y%m%d%H%M%S', returnStdout: true).trim()
                    def branch = env.PIPELINE_BRANCH.replaceAll(/[^A-Za-z0-9_.-]/, '-').take(24)
                    env.PACKAGE_VERSION = "${baseVersion}.${date}-${branch}-${env.SOURCE_COMMIT.take(12)}-${env.BUILD_NUMBER}"
                    env.CI_RUN_ID = "${env.JOB_NAME}-${env.BUILD_NUMBER}-${env.SOURCE_COMMIT}"
                    def publicRegistry = env.PIPELINE_BRANCH in ['test', 'release']
                    env.REGISTRY_HOST = publicRegistry ? 'registry.cn-hangzhou.aliyuncs.com' : 'registry.jingan.com:32008'
                    env.IMAGE_REGISTRY = "${env.REGISTRY_HOST}/${publicRegistry ? 'jingansi' : 'ja'}"
                    env.REGISTRY_CREDENTIAL = publicRegistry ? 'public-aliyun-registry' : 'registry-jingan-com'
                    currentBuild.displayName = "#${env.BUILD_NUMBER} ${env.PIPELINE_BRANCH} ${env.SOURCE_COMMIT.take(12)}"
                }
                sh '''#!/usr/bin/env bash
set -euo pipefail
mkdir -p out/source
git archive --format=tar.gz -o out/source/source.tar.gz HEAD
(cd out/source && sha256sum source.tar.gz > source.sha256)
printf '%s\n' "$SOURCE_COMMIT" > out/source/commit.txt
'''
                stash name: 'selected-source', includes: 'out/source/*'
            }
        }
        stage('Build on Linux') {
            agent { label params.BUILD_AGENT_LABEL }
            stages {
                stage('Unpack exact source') {
                    steps {
                        deleteDir()
                        unstash 'selected-source'
                        sh '''#!/usr/bin/env bash
set -euo pipefail
(cd out/source && sha256sum -c source.sha256)
[[ "$(cat out/source/commit.txt)" == "$SOURCE_COMMIT" ]]
tar -xzf out/source/source.tar.gz
docker version
python3 --version
bash -n ci-package.sh scripts/ci-verify.sh ci/deploy-dev.sh
python3 -m unittest discover -s tests/ci -p 'test_*.py' -v
'''
                    }
                }
                stage('Security and end-to-end gates') {
                    steps { sh 'bash scripts/ci-verify.sh' }
                }
                stage('Package amd64 and arm64') {
                    steps {
                        script {
                            if (params.PUBLISH_IMAGE) {
                                def dockerConfig = "${pwd(tmp: true)}/registry-${env.BUILD_NUMBER}"
                                withEnv(["DOCKER_CONFIG=${dockerConfig}"]) {
                                    try {
                                        withCredentials([usernamePassword(credentialsId: env.REGISTRY_CREDENTIAL, usernameVariable: 'REGISTRY_USER', passwordVariable: 'REGISTRY_PASSWORD')]) {
                                            sh '''#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077
mkdir -p "$DOCKER_CONFIG"
printf '%s' "$REGISTRY_PASSWORD" | docker login "$REGISTRY_HOST" -u "$REGISTRY_USER" --password-stdin
'''
                                            buildPackages(true)
                                        }
                                    } finally {
                                        sh 'rm -rf -- "$DOCKER_CONFIG"'
                                    }
                                }
                            } else {
                                buildPackages(false)
                            }
                        }
                    }
                }
                stage('Upgrade dev service on 105') {
                    when { expression { params.DEPLOY_DEV } }
                    steps {
                        script {
                            withCredentials([
                                usernamePassword(credentialsId: 'ssh-105', usernameVariable: 'DEPLOY_USER', passwordVariable: 'DEPLOY_PASSWORD')
                            ]) {
                                sh 'bash ci/deploy-dev.sh'
                            }
                        }
                    }
                }
            }
            post {
                always {
                    archiveArtifacts artifacts: 'out/source/*,out/verify/**,out/release/**/*.tar.gz,out/release/**/*.sha256,out/release/**/*.json,out/deployment.json', allowEmptyArchive: true, fingerprint: true
                }
            }
        }
    }
    post {
        always { echo "Result=${currentBuild.currentResult}; source=${env.SOURCE_COMMIT}; version=${env.PACKAGE_VERSION}" }
    }
}
