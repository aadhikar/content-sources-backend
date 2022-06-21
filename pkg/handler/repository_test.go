package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/content-services/content-sources-backend/pkg/api"
	"github.com/content-services/content-sources-backend/pkg/dao"
	"github.com/content-services/content-sources-backend/pkg/db"
	"github.com/content-services/content-sources-backend/pkg/models"
	"github.com/labstack/echo/v4"
	"github.com/redhatinsights/platform-go-middlewares/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"gorm.io/gorm"
)

type MockRepositoryDao struct {
	mock.Mock
}

func (r *MockRepositoryDao) Create(newRepo api.RepositoryRequest) error {
	args := r.Called(newRepo)
	return args.Error(0)
}

func (r *MockRepositoryDao) Update(orgID string, uuid string, repoParams api.RepositoryRequest) error {
	args := r.Called(orgID, uuid, repoParams)
	return args.Error(0)
}

func (r *MockRepositoryDao) Fetch(orgID string, uuid string) (api.RepositoryResponse, error) {
	args := r.Called(orgID, uuid)
	if args.Get(0) == nil {
		return api.RepositoryResponse{}, args.Error(0)
	}
	rr, ok := args.Get(0).(api.RepositoryResponse)
	if ok {
		return rr, args.Error(1)
	} else {
		return api.RepositoryResponse{}, args.Error(1)
	}
}

func (r *MockRepositoryDao) List(
	orgID string,
	pageData api.PaginationData,
	filterData api.FilterData,
) (api.RepositoryCollectionResponse, int64, error) {
	args := r.Called(orgID, pageData.Limit, pageData.Offset)
	if args.Get(0) == nil {
		return api.RepositoryCollectionResponse{}, int64(0), args.Error(0)
	}
	rr, ok := args.Get(0).(api.RepositoryCollectionResponse)
	total, okTotal := args.Get(1).(int64)
	if ok && okTotal {
		return rr, total, args.Error(2)
	} else {
		return api.RepositoryCollectionResponse{}, int64(0), args.Error(2)
	}
}

func (r *MockRepositoryDao) Delete(orgID string, uuid string) error {
	args := r.Called(orgID, uuid)
	return args.Error(0)
}

const mockAccountNumber = "0000"
const mockOrgId = "1111"

func encodedIdentity(t *testing.T) string {
	mockIdentity := identity.XRHID{
		Identity: identity.Identity{
			AccountNumber: mockAccountNumber,
			Internal: identity.Internal{
				OrgID: mockOrgId,
			},
		},
	}
	jsonIdentity, err := json.Marshal(mockIdentity)
	if err != nil {
		t.Error("Could not marshal JSON")
	}
	return base64.StdEncoding.EncodeToString(jsonIdentity)
}

func createRepoRequest(name string, url string) api.RepositoryRequest {
	blank := ""
	account := mockAccountNumber
	org := mockOrgId
	return api.RepositoryRequest{
		UUID:      &blank,
		Name:      &name,
		URL:       &url,
		AccountID: &account,
		OrgID:     &org,
	}
}

func createRepoCollection(size, limit, offset int) api.RepositoryCollectionResponse {
	repos := make([]api.RepositoryResponse, size)
	for i := 0; i < size; i++ {
		repo := api.RepositoryResponse{
			UUID:                 fmt.Sprintf("%d", i),
			Name:                 fmt.Sprintf("repo_%d", i),
			URL:                  fmt.Sprintf("http://repo-%d.com", i),
			DistributionVersions: []string{"1"},
			DistributionArch:     "x86_64",
			AccountID:            mockAccountNumber,
			OrgID:                mockOrgId,
		}
		repos[i] = repo
	}
	collection := api.RepositoryCollectionResponse{
		Data: repos,
	}
	params := fmt.Sprintf("?offset=%d&limit=%d", offset, limit)
	setCollectionResponseMetadata(&collection, getTestContext(params), int64(size))
	return collection
}

func serveRepositoriesRouter(req *http.Request, mockDao *MockRepositoryDao) (int, []byte, error) {
	router := echo.New()
	pathPrefix := router.Group(fullRootPath())

	rh := RepositoryHandler{
		RepositoryDao: mockDao,
	}
	RegisterRepositoryRoutes(pathPrefix, &rh.RepositoryDao)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	response := rr.Result()
	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	return response.StatusCode, body, err
}

type ReposSuite struct {
	suite.Suite
	savedDB *gorm.DB
}

func (suite *ReposSuite) SetupTest() {
	suite.savedDB = db.DB
	db.DB = db.DB.Begin()
	db.DB.Where("1=1").Delete(models.RepositoryConfiguration{})
}

func (suite *ReposSuite) TearDownTest() {
	//Rollback and reset db.DB
	db.DB.Rollback()
	db.DB = suite.savedDB
}

func (suite *ReposSuite) TestSimple() {
	t := suite.T()

	mockDao := MockRepositoryDao{}

	collection := createRepoCollection(1, 10, 0)
	mockDao.On("List", mockOrgId, 10, 0).Return(collection, int64(1), nil)

	path := fmt.Sprintf("%s/repositories/?limit=%d", fullRootPath(), 10)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, body, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)

	response := api.RepositoryCollectionResponse{}
	err = json.Unmarshal(body, &response)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 0, response.Meta.Offset)
	assert.Equal(t, int64(1), response.Meta.Count)
	assert.Equal(t, 10, response.Meta.Limit)
	assert.Equal(t, 1, len(response.Data))
	assert.Equal(t, collection.Data[0].Name, response.Data[0].Name)
	assert.Equal(t, collection.Data[0].URL, response.Data[0].URL)
	assert.Equal(t, collection.Data[0].AccountID, response.Data[0].AccountID)

}

func (suite *ReposSuite) TestListNoRepositories() {
	t := suite.T()

	mockDao := MockRepositoryDao{}

	collection := api.RepositoryCollectionResponse{}
	mockDao.On("List", mockOrgId, 100, 0).Return(collection, int64(0), nil)

	req := httptest.NewRequest(http.MethodGet, fullRootPath()+"/repositories/", nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, body, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)

	response := api.RepositoryCollectionResponse{}
	err = json.Unmarshal(body, &response)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, 0, response.Meta.Offset)
	assert.Equal(t, int64(0), response.Meta.Count)
	assert.Equal(t, 100, response.Meta.Limit)
	assert.Equal(t, 0, len(response.Data))
	assert.Equal(t, fullRootPath()+"/repositories/?limit=100&offset=0", response.Links.Last)
	assert.Equal(t, fullRootPath()+"/repositories/?limit=100&offset=0", response.Links.First)
}

func (suite *ReposSuite) TestListPagedExtraRemaining() {
	t := suite.T()

	mockDao := MockRepositoryDao{}

	collection := api.RepositoryCollectionResponse{}
	mockDao.On("List", mockOrgId, 10, 0).Return(collection, int64(102), nil).Once()
	mockDao.On("List", mockOrgId, 10, 100).Return(collection, int64(102), nil).Once()

	path := fmt.Sprintf("%s/repositories/?limit=%d", fullRootPath(), 10)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, body, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)

	response := api.RepositoryCollectionResponse{}
	err = json.Unmarshal(body, &response)
	assert.Nil(t, err)
	assert.Equal(t, 0, response.Meta.Offset)
	assert.Equal(t, 10, response.Meta.Limit)
	assert.Equal(t, int64(102), response.Meta.Count)
	assert.NotEmpty(t, response.Links.Last)

	// Fetch last page
	req = httptest.NewRequest(http.MethodGet, response.Links.Last, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))
	code, body, err = serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)

	response = api.RepositoryCollectionResponse{}
	err = json.Unmarshal(body, &response)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
}

func (suite *ReposSuite) TestListPagedNoRemaining() {
	t := suite.T()

	mockDao := MockRepositoryDao{}

	collection := api.RepositoryCollectionResponse{}
	mockDao.On("List", mockOrgId, 10, 0).Return(collection, int64(100), nil)
	mockDao.On("List", mockOrgId, 10, 90).Return(collection, int64(100), nil)

	path := fmt.Sprintf("%s/repositories/?limit=%d", fullRootPath(), 10)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, body, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)

	response := api.RepositoryCollectionResponse{}
	err = json.Unmarshal(body, &response)
	assert.Nil(t, err)
	assert.Equal(t, 0, response.Meta.Offset)
	assert.Equal(t, 10, response.Meta.Limit)
	assert.Equal(t, int64(100), response.Meta.Count)
	assert.NotEmpty(t, response.Links.Last)

	// Fetch last page
	req = httptest.NewRequest(http.MethodGet, response.Links.Last, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))
	code, body, err = serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)

	response = api.RepositoryCollectionResponse{}
	err = json.Unmarshal(body, &response)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
}

func (suite *ReposSuite) TestFetch() {
	t := suite.T()

	uuid := "abcadaba"
	repo := api.RepositoryResponse{
		Name: "my repo",
		URL:  "https://example.com",
		UUID: uuid,
	}

	mockDao := MockRepositoryDao{}
	mockDao.On("Fetch", mockOrgId, uuid).Return(repo, nil)

	body, err := json.Marshal(repo)
	if err != nil {
		t.Error("Could not marshal JSON")
	}

	req := httptest.NewRequest(http.MethodGet, fullRootPath()+"/repositories/"+uuid,
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	assert.Equal(t, http.StatusOK, code)
}

func (suite *ReposSuite) TestFetchNotFound() {
	t := suite.T()

	uuid := "abcadaba"
	repo := api.RepositoryResponse{
		Name: "my repo",
		URL:  "https://example.com",
		UUID: uuid,
	}

	mockDao := MockRepositoryDao{}
	daoError := dao.Error{
		NotFound: true,
		Message:  "Not found",
	}
	mockDao.On("Fetch", mockOrgId, uuid).Return(api.RepositoryResponse{}, &daoError)

	body, err := json.Marshal(repo)
	if err != nil {
		t.Error("Could not marshal JSON")
	}

	req := httptest.NewRequest(http.MethodGet, fullRootPath()+"/repositories/"+uuid,
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, _ := serveRepositoriesRouter(req, &mockDao)
	assert.Equal(t, http.StatusNotFound, code)
}

func (suite *ReposSuite) TestCreate() {
	repo := api.RepositoryResponse{
		Name: "my repo",
		URL:  "https://example.com",
	}

	expected := createRepoRequest("my repo", "https://example.com")
	expected.FillDefaults()

	mockDao := MockRepositoryDao{}
	mockDao.On("Create", expected).Return(nil)

	t := suite.T()

	body, err := json.Marshal(repo)
	if err != nil {
		t.Error("Could not marshal JSON")
	}

	req := httptest.NewRequest(http.MethodPost, fullRootPath()+"/repositories/",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusCreated, code)
}

func (suite *ReposSuite) TestCreateAlreadyExists() {
	t := suite.T()

	repo := createRepoRequest("my repo", "https://example.com")
	repo.FillDefaults()
	mockDao := MockRepositoryDao{}
	daoError := dao.Error{
		BadValidation: true,
		Message:       "Already exists",
	}
	mockDao.On("Create", repo).Return(&daoError)

	body, err := json.Marshal(repo)
	if err != nil {
		t.Error("Could not marshal JSON")
	}
	req := httptest.NewRequest(http.MethodPost, fullRootPath()+"/repositories/",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusBadRequest, code)
}

func (suite *ReposSuite) TestDelete() {
	t := suite.T()

	uuid := "valid-uuid"
	mockDao := MockRepositoryDao{}
	mockDao.On("Delete", mockOrgId, uuid).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, fullRootPath()+"/repositories/"+uuid, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusNoContent, code)
}

func (suite *ReposSuite) TestDeleteNotFound() {
	t := suite.T()

	uuid := "invalid-uuid"
	mockDao := MockRepositoryDao{}
	daoError := dao.Error{
		NotFound: true,
	}
	mockDao.On("Delete", mockOrgId, uuid).Return(&daoError)

	req := httptest.NewRequest(http.MethodDelete, fullRootPath()+"/repositories/"+uuid, nil)
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusNotFound, code)
}

func (suite *ReposSuite) TestFullUpdate() {
	t := suite.T()

	uuid := "someuuid"
	request := createRepoRequest("Some Name", "http://someurl.com")
	expected := createRepoRequest(*request.Name, *request.URL)
	expected.FillDefaults()

	mockDao := MockRepositoryDao{}
	mockDao.On("Update", mockOrgId, uuid, expected).Return(nil)

	body, err := json.Marshal(request)
	if err != nil {
		t.Error("Could not marshal JSON")
	}

	req := httptest.NewRequest(http.MethodPut, fullRootPath()+"/repositories/"+uuid,
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusOK, code)
}

func (suite *ReposSuite) TestPartialUpdate() {
	t := suite.T()

	uuid := "someuuid"
	request := createRepoRequest("Some Name", "http://someurl.com")
	expected := createRepoRequest(*request.Name, *request.URL)

	mockDao := MockRepositoryDao{}
	mockDao.On("Update", mockOrgId, uuid, expected).Return(nil)

	body, err := json.Marshal(request)
	if err != nil {
		t.Error("Could not marshal JSON")
	}

	req := httptest.NewRequest(http.MethodPatch, fullRootPath()+"/repositories/"+uuid,
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(api.IdentityHeader, encodedIdentity(t))

	code, _, err := serveRepositoriesRouter(req, &mockDao)
	assert.Nil(t, err)
	mockDao.AssertExpectations(t)
	assert.Equal(t, http.StatusOK, code)
}

func TestReposSuite(t *testing.T) {
	suite.Run(t, new(ReposSuite))
}
