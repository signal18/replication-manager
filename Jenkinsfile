node () {
	stage ('Checkout') {
        checkout([$class: 'GitSCM', branches: [[name: 'refs/heads/develop']], doGenerateSubmoduleConfigurations: false, extensions: [], submoduleCfg: [], userRemoteConfigs: [[credentialsId: '', url: 'https://github.com/signal18/replication-manager.git']]]) 
	}
    stage ('Build OSC') {
        docker.withRegistry("https://index.docker.io/v1/", "docker-hub") {
	        def newApp = docker.build("signal18/replication-manager:2.3","-f docker/Dockerfile .")
            newApp.push()
            newApp.push 'latest'
	    }
	}
    stage ('Build PRO') {
        docker.withRegistry("https://index.docker.io/v1/", "docker-hub") {
	        def newApp = docker.build("signal18/replication-manager:2.3-pro","-f docker/Dockerfile.pro .")
            newApp.push()
	    }
	}
}
