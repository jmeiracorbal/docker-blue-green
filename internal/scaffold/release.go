package scaffold

// publishWorkflowFile installs a thin, version-pinned caller for the shared
// registry publishing workflow. No registry credentials are stored in the repository.
func publishWorkflowFile() File {
	return File{
		Path: ".github/workflows/release.yml",
		Data: []byte(`name: Publish OCI images

on:
  release:
    types: [published]

permissions:
  contents: read
  packages: write

jobs:
  publish:
    uses: jmeiracorbal/docker-blue-green/.github/workflows/publish-images.yml@ad39017c75238e96e9eaadbd1fe4fb0cf16458c5
    with:
      oci_registry_host: ${{ vars.OCI_REGISTRY_HOST }}
      oci_registry_repository: ${{ vars.OCI_REGISTRY_REPOSITORY }}
      oci_registry_username: ${{ vars.OCI_REGISTRY_USERNAME }}
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
    secrets:
      OCI_REGISTRY_TOKEN: ${{ secrets.OCI_REGISTRY_TOKEN }}

  deploy:
    if: ${{ vars.DEPLOY_ENABLED == 'true' }}
    needs: publish
    uses: jmeiracorbal/docker-blue-green/.github/workflows/deploy-remote.yml@534cba82805c78e9cc3ccea28e8b15ed1cc3f8f3
    with:
      project: ${{ vars.DEPLOY_PROJECT }}
      version: ${{ github.event.release.tag_name }}
      source_ref: ${{ github.event.release.tag_name }}
      engine_ref: 534cba82805c78e9cc3ccea28e8b15ed1cc3f8f3
      deploy_host: ${{ vars.DEPLOY_HOST }}
      deploy_user: ${{ vars.DEPLOY_USER }}
      deploy_port: ${{ fromJSON(vars.DEPLOY_PORT) }}
      deploy_ssh_known_hosts: ${{ vars.DEPLOY_SSH_KNOWN_HOSTS }}
    secrets:
      SERVER_SSH_PRIVATE_KEY: ${{ secrets.SERVER_SSH_PRIVATE_KEY }}
      OCI_REGISTRY_PULL_USERNAME: ${{ secrets.OCI_REGISTRY_PULL_USERNAME }}
      OCI_REGISTRY_PULL_TOKEN: ${{ secrets.OCI_REGISTRY_PULL_TOKEN }}
`),
	}
}
