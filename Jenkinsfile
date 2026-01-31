pipeline {
    agent any
    stages {
        stage('Checkout') {
            steps {
                checkout([$class: 'GitSCM', branches: [[name: 'refs/heads/develop']], doGenerateSubmoduleConfigurations: false, extensions: [], submoduleCfg: [], userRemoteConfigs: [[credentialsId: '', url: 'https://github.com/signal18/replication-manager.git']]])
            }
        }
        stage('Build OSC') {
            when { buildingTag() }
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build("signal18/replication-manager:${env.TAG_NAME}", '-f docker/Dockerfile .')
                        Image.push('latest')
                        Image.push(env.TAG_NAME)
                    }
                }
            }
        }
        stage('Build OSC Rootless') {
            when { buildingTag() }
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build("signal18/replication-manager:${env.TAG_NAME}-rootless", '-f docker/Dockerfile_rootless .')
                        Image.push('latest-rootless')
                        Image.push(env.TAG_NAME+'-rootless')
                    }
                }
            }
        }
        stage('Build PRO') {
            when { buildingTag() }
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build("signal18/replication-manager:${env.TAG_NAME}-pro", '-f docker/Dockerfile.pro .')
                        Image.push(env.TAG_NAME+'-pro')
                    }
                }
            }
        }
        stage('Build PRO Rootless') {
            when { buildingTag() }
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build("signal18/replication-manager:pro-rootless", '-f docker/Dockerfile.pro_rootless .')
                        Image.push('pro-rootless')
                        Image.push(env.TAG_NAME+'-pro-rootless')
                    }
                }
            }
        }
        stage('Build nightly') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:nightly', '-f docker/Dockerfile.pro .')
                        Image.push('nightly')
                    }
                }
            }
        }
        stage('Build nightly rootless') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:nightly-rootless', '-f docker/Dockerfile.pro_rootless .')
                        Image.push('nightly-rootless')
                    }
                }
            }
        }
        stage('Build DEV') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:dev', '-f docker/Dockerfile.dev .')
                        Image.push('dev')
                        if (env.TAG_NAME) {
                            Image.push(env.TAG_NAME+'-dev')
                        }
                    }
                }
            }
        }
        stage('Build DEV Rootless') {
            steps {
                script {
                    docker.withRegistry('https://index.docker.io/v1/', 'docker-hub') {
                        def Image = docker.build('signal18/replication-manager:dev-rootless', '-f docker/Dockerfile.dev_rootless .')
                        Image.push('dev-rootless')
                        if (env.TAG_NAME) {
                            Image.push(env.TAG_NAME+'-dev-rootless')
                        }
                    }
                }
            }
        }
    }
    post {
        failure {
            script {
                mattermostSend(
                    color: '#FF0000',
                    message: "Build failed! Job: `${JOB_NAME}` Build: `${BUILD_NUMBER}` (<${env.BUILD_URL}|Open>)"
                )
            }
        }
        success {
            script {
                sh 'docker system prune --force'
            }
        }
    }
}
