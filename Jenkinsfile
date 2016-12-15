#!/usr/bin/groovy
@Library('github.com/rawlingsj/fabric8-pipeline-library@master')
def dummy
goNode{
  dockerNode{  
    def v = goRelease{
      githubOrganisation = 'fabric8io'
      dockerOrganisation = 'fabric8'
      project = 'configmapcontroller'
    }

    stage ('Update downstream dependencies') {

    updateDownstreamDependencies(v)
    }
  }
}

def updateDownstreamDependencies(v) {
  pushPomPropertyChangePR {
    propertyName = 'configmapcontroller.version'
    projects = [
            'fabric8io/fabric8-devops'
    ]
    version = v
  }
}