#!/usr/bin/groovy
node{

  git 'https://github.com/fabric8io/configmapcontroller.git'

  kubernetes.pod('buildpod').withImage('fabric8/go-builder')
  .withEnvVar('GOPATH','/home/jenkins/workspace/workspace/go')
  .withPrivileged(true).inside {

    stage 'build binary'
    
    sh """
    mkdir -p ../go/bin;
    mkdir -p ../go/src/github.com/fabric8io/configmapcontroller; 
    cp -R ../${env.JOB_NAME}/. ../go/src/github.com/fabric8io/configmapcontroller/; 
    cd ../go/src/github.com/fabric8io/configmapcontroller; make
    """

    sh "cp -R ../go/src/github.com/fabric8io/configmapcontroller/out ."

    def imageName = 'configmapcontroller'
    def tag = 'latest'

    stage 'build image'
    kubernetes.image().withName(imageName).build().fromPath(".")

    stage 'tag'
    kubernetes.image().withName(imageName).tag().inRepository('docker.io/fabric8/'+imageName).force().withTag(tag)

    stage 'push'
    kubernetes.image().withName('docker.io/fabric8/'+imageName).push().withTag(tag).toRegistry()

  }
}
