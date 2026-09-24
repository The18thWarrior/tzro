//go:build integration

package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func scaffoldTSMonorepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"package.json": `{
  "name": "ts-monorepo",
  "private": true,
  "workspaces": ["packages/*"]
}`,
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "ESNext",
    "module": "CommonJS",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true
  }
}`,
		".eslintrc.json": `{
  "extends": ["eslint:recommended", "plugin:@typescript-eslint/recommended"]
}`,
		"packages/ui/src/App.tsx": `import React from 'react';
import { AuthProvider } from './context/AuthContext';
import { Dashboard } from './components/Dashboard';

export const App = () => (
  <AuthProvider>
    <Dashboard />
  </AuthProvider>
);`,
		"packages/ui/src/hooks/useAuth.ts": `import { useContext, useState } from 'react';
import { AuthContext } from '../context/AuthContext';

export const useAuth = () => {
  const ctx = useContext(AuthContext);
  const [token, setToken] = useState<string | null>(null);

  const login = async (username: string) => {
      // simulated login
      setToken("new_token_123");
  };

  const logout = () => {
      setToken(null);
  };

  const isExpired = () => {
      return false;
  };

  // BUG: Stale token after refresh. We don't update context properly.
  const refresh = () => {
    setToken("refreshed_token_but_not_in_context");
  };

  return { ...ctx, login, logout, refresh, isExpired, token };
};`,
		"packages/ui/src/context/AuthContext.tsx": `import React, { createContext } from 'react';

export const AuthContext = createContext({ user: null, token: null as string | null });
export const AuthProvider: React.FC<{children: React.ReactNode}> = ({children}) => {
  return <AuthContext.Provider value={{ user: null, token: null }}>{children}</AuthContext.Provider>;
};`,
		"packages/ui/src/components/Dashboard.tsx": `import React from 'react';
import { useAuth } from '../hooks/useAuth';

export const Dashboard = () => {
  const { user, token } = useAuth();
  if (!user) return <div>Please log in</div>;
  return <div>Welcome {user.name} - Token: {token}</div>;
};`,
		"packages/ui/src/components/LoginForm.tsx": `import React, { useState } from 'react';
import { useAuth } from '../hooks/useAuth';

export const LoginForm = () => {
    const [val, setVal] = useState('');
    const { login } = useAuth();
    const onSubmit = (e: any) => {
        e.preventDefault();
        login(val);
    };
    const onChange = (e: any) => setVal(e.target.value);
    return <form onSubmit={onSubmit}><input type="text" onChange={onChange}/></form>;
};`,
		"packages/api/src/index.ts": `import express from 'express';
import { usersRouter } from './routes/users';
import { productsRouter } from './routes/products';

const app = express();
app.use('/users', usersRouter);
app.use('/products', productsRouter);
app.listen(3000, () => console.log('started'));`,
		"packages/api/src/middleware/auth.ts": `import { Request, Response, NextFunction } from 'express';

export const authMiddleware = (req: Request, res: Response, next: NextFunction) => {
  if (!req.headers.authorization) {
    // BUG: Wrong status code (should be 401, not 200)
    return res.status(200).json({ error: 'unauthorized' });
  }
  next();
};`,
		"packages/api/src/routes/users.ts": `import { Router } from 'express';
import { getUser, createUser, deleteUser } from '../controllers/userController';
export const usersRouter = Router();
usersRouter.get('/:id', getUser);
usersRouter.post('/', createUser);
usersRouter.delete('/:id', deleteUser);`,
		"packages/api/src/routes/products.ts": `import { Router } from 'express';
export const productsRouter = Router();
productsRouter.get('/', (req, res) => res.json([]));
productsRouter.post('/', (req, res) => res.status(201).json({}));`,
		"packages/api/src/controllers/userController.ts": `import { Request, Response } from 'express';
export const getUser = (req: Request, res: Response) => {
  res.json({ id: req.params.id, name: 'Alice' });
};
export const createUser = (req: Request, res: Response) => {
  res.status(201).json({ id: '123', name: req.body.name });
};
export const deleteUser = (req: Request, res: Response) => {
  res.status(204).send();
};`,
		"packages/shared/src/types.ts": `// BUG: Type mismatch between frontend and backend
export interface User {
  id: string; // Frontend expects string
  name: string;
}
export interface Product { id: string; price: number; }`,
		"packages/shared/src/validation.ts": `export const isValid = (obj: any) => !!obj;
export const isUserValid = (u: any) => u && u.id && u.name;
export const isProductValid = (p: any) => p && p.id && p.price > 0;
`,
		"packages/shared/src/constants.ts": `export const API_URL = 'http://localhost:3000';
export const IS_PRODUCTION: boolean = true;`,
		"README.md": `# TS Monorepo
Known issues:
- Authentication seems to drop the token after a refresh.
- API is returning 200 OK for unauthorized requests.
- Type mismatches between User definitions on the frontend vs what's coming from the backend.`,
		"logs/jest_output.log": `FAIL packages/ui/src/__tests__/Auth.test.tsx\n  \u25cf Auth flow \u203a should update token after refresh\n\n    expect(received).toBe(expected) // Object.is equality\n\n    Expected: \"new_token_123\"\n    Received: null\n\n      22 |     const { result } = renderHook(() => useAuth(), { wrapper: AuthProvider });\n      23 |     act(() => result.current.refresh());\n    > 24 |     expect(result.current.token).toBe(\"new_token_123\");\n         |                                  ^\n      25 |   });\n\n      at Object.<anonymous> (packages/ui/src/__tests__/Auth.test.tsx:24:34)\n      at Promise.then.completed (node_modules/jest-circus/build/utils.js:391:28)\n      at new Promise (<anonymous>)\n      at callAsyncCircusFn (node_modules/jest-circus/build/utils.js:316:10)\n      at _callCircusTest (node_modules/jest-circus/build/run.js:218:40)\n      at processTicksAndRejections (node:internal/process/task_queues:95:5)\n      at _runTest (node_modules/jest-circus/build/run.js:155:3)\n      at _runTestsForDescribeBlock (node_modules/jest-circus/build/run.js:66:9)\n      at _runTestsForDescribeBlock (node_modules/jest-circus/build/run.js:60:9)\n      at run (node_modules/jest-circus/build/run.js:25:3)\n      at runAndTransformResultsToJestFormat (node_modules/jest-circus/build/legacy-code-todo-rewrite/jestAdapterInit.js:170:21)\n      at jestAdapter (node_modules/jest-circus/build/legacy-code-todo-rewrite/jestAdapter.js:82:19)\n      at runTestInternal (node_modules/jest-runner/build/runTest.js:389:16)\n      at runTest (node_modules/jest-runner/build/runTest.js:475:34)\n\nFAIL packages/api/src/__tests__/middleware.test.ts\n  \u25cf Auth Middleware \u203a returns 401 when no token provided\n\n    expect(received).toBe(expected)\n\n    Expected: 401\n    Received: 200\n\n      12 |     const req = mockRequest({});\n      13 |     authMiddleware(req, res, next);\n    > 14 |     expect(res.status).toBe(401);\n         |                        ^\n      15 |   });\n\n      at Object.<anonymous> (packages/api/src/__tests__/middleware.test.ts:14:24)\n      at Promise.then.completed (node_modules/jest-circus/build/utils.js:391:28)\n      at new Promise (<anonymous>)\n      at callAsyncCircusFn (node_modules/jest-circus/build/utils.js:316:10)\n      at _callCircusTest (node_modules/jest-circus/build/run.js:218:40)\n      at processTicksAndRejections (node:internal/process/task_queues:95:5)\n      at _runTest (node_modules/jest-circus/build/run.js:155:3)\n      at _runTestsForDescribeBlock (node_modules/jest-circus/build/run.js:66:9)\n      at run (node_modules/jest-circus/build/run.js:25:3)\n      at runAndTransformResultsToJestFormat (node_modules/jest-circus/build/legacy-code-todo-rewrite/jestAdapterInit.js:170:21)\n      at jestAdapter (node_modules/jest-circus/build/legacy-code-todo-rewrite/jestAdapter.js:82:19)\n      at runTestInternal (node_modules/jest-runner/build/runTest.js:389:16)\n      at runTest (node_modules/jest-runner/build/runTest.js:475:34)\n\nFAIL packages/shared/src/__tests__/validation.test.ts\n  \u25cf User Validation \u203a handles missing fields gracefully\n\n    TypeError: Cannot read properties of undefined (reading 'id')\n\n      8 |   it('handles missing fields gracefully', () => {\n      9 |     const invalidUser = { name: \"Test\" } as any;\n    > 10 |     expect(isValid(invalidUser)).toBe(false);\n         |            ^\n      11 |   });\n\n      at isValid (packages/shared/src/validation.ts:5:28)\n      at Object.<anonymous> (packages/shared/src/__tests__/validation.test.ts:10:12)\n      at Promise.then.completed (node_modules/jest-circus/build/utils.js:391:28)\n      at new Promise (<anonymous>)\n      at callAsyncCircusFn (node_modules/jest-circus/build/utils.js:316:10)\n      at _callCircusTest (node_modules/jest-circus/build/run.js:218:40)\n      at processTicksAndRejections (node:internal/process/task_queues:95:5)\n      at _runTest (node_modules/jest-circus/build/run.js:155:3)\n      at run (node_modules/jest-circus/build/run.js:25:3)\n      at runAndTransformResultsToJestFormat (node_modules/jest-circus/build/legacy-code-todo-rewrite/jestAdapterInit.js:170:21)\n      at jestAdapter (node_modules/jest-circus/build/legacy-code-todo-rewrite/jestAdapter.js:82:19)\n      at runTestInternal (node_modules/jest-runner/build/runTest.js:389:16)\n      at runTest (node_modules/jest-runner/build/runTest.js:475:34)\n\nPASS packages/ui/src/__tests__/Dashboard.test.tsx\nPASS packages/api/src/__tests__/users.test.ts\nPASS packages/shared/src/__tests__/utils.test.ts\nPASS packages/ui/src/__tests__/LoginForm.test.tsx\n\nTest Suites: 3 failed, 4 passed, 7 total\nTests:       4 failed, 15 passed, 19 total\nSnapshots:   0 total\nTime:        4.567 s\nRan all test suites.\n`,
		"logs/tsc_errors.log":  `packages/ui/src/hooks/useAuth.ts:15:5 - error TS2322: Type 'string' is not assignable to type 'number'.\n\n15     setToken(\"refreshed_token_but_not_in_context\");\n       ~~~~~~~~\n\n  packages/ui/src/context/AuthContext.tsx:5:3\n    5   token: number | null;\n        ~~~~~\n    The expected type comes from property 'token' which is declared here on type 'AuthContextType'\n\npackages/shared/src/types.ts:3:3 - error TS2320: Interface 'User' cannot simultaneously extend types 'Entity' and 'Node'.\n  Named property 'id' of types 'Entity' and 'Node' are not identical.\n\n3   id: string; // Frontend expects string\n    ~~\n\npackages/api/src/middleware/auth.ts:12:15 - error TS2339: Property 'user' does not exist on type 'Request<ParamsDictionary, any, any, ParsedQs, Record<string, any>>'.\n\n12   if (!req.user) {\n                  ~~~~\n\npackages/ui/src/components/Dashboard.tsx:8:23 - error TS2531: Object is possibly 'null'.\n\n8   return <div>{user.name} - Welcome {token}</div>;\n                        ~~~~\n\npackages/api/src/controllers/userController.ts:5:22 - error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.\n\n5   const user = await getUserById(req.params.id);\n                                             ~~~~~~~~~~~~~\n\npackages/shared/src/constants.ts:2:14 - error TS2322: Type 'string' is not assignable to type 'boolean'.\n\n2 export const IS_PRODUCTION: boolean = \"true\";\n               ~~~~~~~~~~~~~\n\npackages/ui/src/App.tsx:10:14 - error TS2769: No overload matches this call.\n  Overload 1 of 2, '(props: DashboardProps): ReactNode', gave the following error.\n\n10     <Dashboard title=\"Home\" />\n                ~~~~~\n\npackages/api/src/routes/products.ts:8:14 - error TS2339: Property 'price' does not exist on type 'Product'.\n\n8   if (product.price < 0) {\n                 ~~~~~\n\npackages/ui/src/components/LoginForm.tsx:15:32 - error TS2339: Property 'value' does not exist on type 'EventTarget'.\n\n15   const onChange = (e) => setVal(e.target.value);\n                                            ~~~~~\n\nFound 9 errors.\n`,
		"logs/eslint_output.log": `packages/api/src/middleware/auth.ts
  5:12  warning  Unexpected status code 200 for unauthorized error.
  
packages/ui/src/hooks/useAuth.ts
  2:10  warning  'useState' is defined but never used.

packages/api/src/controllers/userController.ts
  10:5  warning  Expected a return value.`,
	}

	for relPath, content := range files {
		fullPath := filepath.Join(dir, relPath)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	return dir
}

func scaffoldPythonMLPipeline(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"requirements.txt": `numpy==1.26.0
pandas==2.1.0
torch==2.1.0
pytest==7.4.2
pyyaml==6.0.1
`,
		"setup.py": `from setuptools import setup, find_packages
setup(name='ml_pipeline', packages=find_packages())`,
		"pyproject.toml": `[build-system]
requires = ["setuptools>=61.0"]
build-backend = "setuptools.build_meta"`,
		"src/data/loader.py": `import pandas as pd
def load_data(path): 
    print(f"Loading data from {path}")
    return pd.read_csv(path)
def split_data(df, ratio=0.8):
    n = int(len(df) * ratio)
    return df.iloc[:n], df.iloc[n:]
`,
		"src/data/preprocessor.py": `def preprocess(df):
    df = df.fillna(0)
    df = df.clip(-10, 10)
    return df
def normalize(df):
    return (df - df.mean()) / (df.std() + 1e-9)
`,
		"src/data/augmentation.py": `def augment(x): return x`,
		"src/models/classifier.py": `import torch.nn as nn
class Classifier(nn.Module):
    def __init__(self, dropout=0.3):
        super().__init__()
        self.fc1 = nn.Linear(10, 128)
        self.relu = nn.ReLU()
        self.drop = nn.Dropout(dropout)
        self.fc2 = nn.Linear(128, 2)
    def forward(self, x):
        return self.fc2(self.drop(self.relu(self.fc1(x))))`,
		"src/models/feature_extractor.py": `def extract_features(x): return x`,
		"src/training/trainer.py": `import torch
def train_epoch(model, optimizer, loader):
    model.train()
    total_loss = 0
    for x, y in loader:
        optimizer.zero_grad()
        out = model(x)
        loss = torch.nn.functional.cross_entropy(out, y)
        loss.backward()
        optimizer.step()
        total_loss += loss.item()
    return total_loss / len(loader)
def save_checkpoint(model, path):
    torch.save(model.state_dict(), path)
def load_checkpoint(model, path):
    model.load_state_dict(torch.load(path))
`,
		"src/training/evaluator.py": `import torch
def evaluate(model, loader):
    model.eval()
    total_loss = 0
    correct = 0
    with torch.no_grad():
        for x, y in loader:
            out = model(x)
            loss = torch.nn.functional.cross_entropy(out, y)
            total_loss += loss.item()
            pred = out.argmax(dim=1)
            correct += (pred == y).sum().item()
    return {"loss": total_loss / len(loader), "accuracy": correct / (len(loader)*32)}
`,
		"src/training/config.py": `import yaml
def load_config(path):
    with open(path) as f: return yaml.safe_load(f)`,
		"src/utils/metrics.py": `def compute_accuracy(y_true, y_pred): return 0.9
def compute_f1(y_true, y_pred): return 0.85
def compute_auc(y_true, y_pred): return 0.92`,
		"src/utils/logging_utils.py": `import logging
def setup_logger(): logging.basicConfig(level=logging.INFO)`,
		"configs/baseline.yaml": `learning_rate: 0.001
dropout: 0.3
batch_size: 32`,
		"configs/experiment_v2.yaml": `# BUG: Bad hyperparameters causing accuracy drop
learning_rate: 0.1
dropout: 0.0
batch_size: 32`,
		"tests/test_preprocessor.py": `def test_preprocessor(): assert True`,
		"tests/test_classifier.py": `import torch
from src.models.classifier import Classifier
def test_classifier_output():
    c = Classifier(dropout=0.3)
    x = torch.randn(1, 10)
    out = c(x)
    assert out.shape == (1, 2)`,
		"README.md": `# ML Pipeline
Known issues:
- Model accuracy dropped from 92% to 45% when we switched from baseline.yaml to experiment_v2.yaml.
- Check configs or the classifier structure to see what caused the regression.`,
		"logs/training.log":      `[INFO] 2026-09-22 10:00:01 - Initializing PyTorch distributed backend...\n[INFO] 2026-09-22 10:00:02 - Backend initialized: nccl\n[INFO] 2026-09-22 10:00:02 - Loading configuration from configs/experiment_v2.yaml\n[INFO] 2026-09-22 10:00:02 - Configuration loaded: {'learning_rate': 0.1, 'dropout': 0.0, 'batch_size': 32}\n[INFO] 2026-09-22 10:00:02 - Setting up data loaders...\n[INFO] 2026-09-22 10:00:04 - Data loaders ready. Train batches: 1250, Val batches: 312\n[INFO] 2026-09-22 10:00:04 - Initializing model architecture Classifier...\n[INFO] 2026-09-22 10:00:04 - Model summary:\nClassifier(\n  (fc1): Linear(in_features=10, out_features=128, bias=True)\n  (relu): ReLU()\n  (drop): Dropout(p=0.0, inplace=False)\n  (fc2): Linear(in_features=128, out_features=2, bias=True)\n)\nTotal parameters: 1,538\n[INFO] 2026-09-22 10:00:04 - Initializing Adam optimizer with lr=0.1\n[INFO] 2026-09-22 10:00:04 - Starting training loop...\nEpoch 1/10\n----------\n[INFO] 2026-09-22 10:00:15 - Batch 500/1250 - loss: 0.6931 - acc: 0.5100\n[INFO] 2026-09-22 10:00:26 - Batch 1000/1250 - loss: 0.6854 - acc: 0.5210\nTrain Loss: 0.6920 Acc: 0.5150\nVal Loss: 0.6800 Acc: 0.5100\n\nEpoch 2/10\n----------\n[INFO] 2026-09-22 10:00:38 - Batch 500/1250 - loss: 8.4120 - acc: 0.4910\n[INFO] 2026-09-22 10:00:49 - Batch 1000/1250 - loss: 12.351 - acc: 0.4850\nTrain Loss: 12.400 Acc: 0.4900\nVal Loss: 25.100 Acc: 0.4800\nWARNING: Validation loss significantly higher than train loss.\n\nEpoch 3/10\n----------\n[INFO] 2026-09-22 10:01:01 - Batch 500/1250 - loss: 30.124 - acc: 0.4700\n[INFO] 2026-09-22 10:01:12 - Batch 1000/1250 - loss: 45.210 - acc: 0.4610\nTrain Loss: 45.200 Acc: 0.4600\nVal Loss: 89.400 Acc: 0.4500\nWARNING: Loss is exploding. Check learning rate or gradient clipping.\n\nEpoch 4/10\n----------\n[INFO] 2026-09-22 10:01:24 - Batch 500/1250 - loss: 102.34 - acc: 0.4500\n[INFO] 2026-09-22 10:01:35 - Batch 1000/1250 - loss: 154.21 - acc: 0.4410\nTrain Loss: 154.20 Acc: 0.4400\nVal Loss: 210.40 Acc: 0.4300\n\nEpoch 5/10\n----------\n[INFO] 2026-09-22 10:01:47 - Batch 500/1250 - loss: 312.45 - acc: 0.4300\n[INFO] 2026-09-22 10:01:58 - Batch 1000/1250 - loss: 415.67 - acc: 0.4200\nTrain Loss: 415.60 Acc: 0.4250\nVal Loss: 512.30 Acc: 0.4100\n\nEpoch 6/10\n----------\n[INFO] 2026-09-22 10:02:10 - Batch 500/1250 - loss: 812.34 - acc: 0.4100\n[INFO] 2026-09-22 10:02:21 - Batch 1000/1250 - loss: 954.21 - acc: 0.4050\nTrain Loss: 954.20 Acc: 0.4080\nVal Loss: 1024.5 Acc: 0.4000\n\nEpoch 7/10\n----------\n[INFO] 2026-09-22 10:02:33 - Batch 500/1250 - loss: 1512.4 - acc: 0.4000\n[INFO] 2026-09-22 10:02:44 - Batch 1000/1250 - loss: 1845.2 - acc: 0.3950\nTrain Loss: 1845.2 Acc: 0.3980\nVal Loss: 2104.3 Acc: 0.3900\n\nEpoch 8/10\n----------\n[INFO] 2026-09-22 10:02:56 - Batch 500/1250 - loss: 2512.4 - acc: 0.3900\n[INFO] 2026-09-22 10:03:07 - Batch 1000/1250 - loss: 2945.2 - acc: 0.3850\nTrain Loss: 2945.2 Acc: 0.3880\nVal Loss: 3204.3 Acc: 0.3800\n\nEpoch 9/10\n----------\n[INFO] 2026-09-22 10:03:19 - Batch 500/1250 - loss: 3812.4 - acc: 0.3800\n[INFO] 2026-09-22 10:03:30 - Batch 1000/1250 - loss: 4145.2 - acc: 0.3750\nTrain Loss: 4145.2 Acc: 0.3780\nVal Loss: 4504.3 Acc: 0.3700\n\nEpoch 10/10\n----------\n[INFO] 2026-09-22 10:03:42 - Batch 500/1250 - loss: 5112.4 - acc: 0.3700\n[INFO] 2026-09-22 10:03:53 - Batch 1000/1250 - loss: 5845.2 - acc: 0.3650\nTrain Loss: 5845.2 Acc: 0.3680\nVal Loss: 6204.3 Acc: 0.3600\n\n[INFO] 2026-09-22 10:04:00 - Training complete. Best val_loss: 0.6800.\n[INFO] 2026-09-22 10:04:00 - Saving model to checkpoints/best_model.pth\n[INFO] 2026-09-22 10:04:01 - Finished execution.\n`,
		"logs/pytest_output.log": `============================= test session starts ==============================\nplatform linux -- Python 3.10.12, pytest-7.4.2, pluggy-1.3.0\nrootdir: /tmp/pytest-of-runner/pytest-0/test_pipeline0\ncollected 4 items\n\ntests/test_classifier.py F\ntests/test_evaluator.py F\ntests/test_preprocessor.py .\ntests/test_trainer.py F\n\n=================================== FAILURES ===================================\n____________________________ test_classifier_output ____________________________\n\n    def test_classifier_output():\n        c = Classifier(dropout=0.3)\n        x = torch.randn(1, 10)\n>       out = c(x)\n\ntests/test_classifier.py:7: \n_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ \nsrc/models/classifier.py:12: in forward\n    return self.fc2(self.drop(self.relu(self.fc1(x))))\n../../../.local/lib/python3.10/site-packages/torch/nn/modules/module.py:1501: in _call_impl\n    return forward_call(*args, **kwargs)\n../../../.local/lib/python3.10/site-packages/torch/nn/modules/linear.py:114: in forward\n    return F.linear(input, self.weight, self.bias)\n_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ \n\ninput = tensor([[ 1.2588,  0.4284, -0.6277, -0.2223, -0.4571,  0.6833, -0.9996,  1.1008,\n         -1.3534, -0.4431]])\nweight = Parameter containing:\ntensor([[ 0.0520,  0.0150, -0.2185,  ..., -0.0632, -0.0135, -0.0241],\n        [-0.0766,  0.0381,  ...      [ 0.1764,  0.1345,  0.0465,  ...,  0.2295, -0.0053, -0.0028]],\n       requires_grad=True)\nbias = Parameter containing:\ntensor([ 0.1337, -0.1601,  0.0353, -0.0309,  0.1878, -0.0906,  0.1555,  0.1273,\n...  -0.1068,  0.1627, -0.2520,  0.2155, -0.0104,  0.2785, -0.1989, -0.2285],\n       requires_grad=True)\n\n    def linear(input: Tensor, weight: Tensor, bias: Optional[Tensor] = None) -> Tensor:\n        r\"\"\"\n        Applies a linear transformation to the incoming data: y = xA^T + b.\n        \"\"\"\n        if has_torch_function_variant(input, weight, bias):\n            return handle_torch_function(linear, (input, weight, bias), input, weight, bias=bias)\n>       return torch._C._nn.linear(input, weight, bias)\nE       RuntimeError: mat1 and mat2 shapes cannot be multiplied (1x10 and 128x2)\n\n../../../.local/lib/python3.10/site-packages/torch/nn/functional.py:1909: RuntimeError\n\n____________________________ test_evaluator_metrics ____________________________\n\n    def test_evaluator_metrics():\n        from src.training.evaluator import evaluate\n        model = Classifier()\n        loader = [ (torch.randn(32, 10), torch.randint(0, 2, (32,))) for _ in range(5) ]\n>       metrics = evaluate(model, loader)\n\ntests/test_evaluator.py:8: \n_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ \nsrc/training/evaluator.py:10: in evaluate\n    out = model(x)\nsrc/models/classifier.py:12: in forward\n    return self.fc2(self.drop(self.relu(self.fc1(x))))\n../../../.local/lib/python3.10/site-packages/torch/nn/modules/module.py:1501: in _call_impl\n    return forward_call(*args, **kwargs)\n../../../.local/lib/python3.10/site-packages/torch/nn/modules/linear.py:114: in forward\n    return F.linear(input, self.weight, self.bias)\n_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ \n>       return torch._C._nn.linear(input, weight, bias)\nE       RuntimeError: mat1 and mat2 shapes cannot be multiplied (32x10 and 128x2)\n\n../../../.local/lib/python3.10/site-packages/torch/nn/functional.py:1909: RuntimeError\n\n____________________________ test_trainer_loop _________________________________\n\n    def test_trainer_loop():\n        from src.training.trainer import train_epoch\n        from torch.optim import Adam\n        model = Classifier()\n        opt = Adam(model.parameters())\n        loader = [ (torch.randn(32, 10), torch.randint(0, 2, (32,))) for _ in range(2) ]\n>       loss = train_epoch(model, opt, loader)\n\ntests/test_trainer.py:11: \n_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ \nsrc/training/trainer.py:8: in train_epoch\n    out = model(x)\nsrc/models/classifier.py:12: in forward\n    return self.fc2(self.drop(self.relu(self.fc1(x))))\n../../../.local/lib/python3.10/site-packages/torch/nn/modules/module.py:1501: in _call_impl\n    return forward_call(*args, **kwargs)\n../../../.local/lib/python3.10/site-packages/torch/nn/modules/linear.py:114: in forward\n    return F.linear(input, self.weight, self.bias)\n_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ \n>       return torch._C._nn.linear(input, weight, bias)\nE       RuntimeError: mat1 and mat2 shapes cannot be multiplied (32x10 and 128x2)\n\n../../../.local/lib/python3.10/site-packages/torch/nn/functional.py:1909: RuntimeError\n\n=========================== short test summary info ============================\nFAILED tests/test_classifier.py::test_classifier_output - RuntimeError: mat1 and mat2 shapes cannot be multiplied (1x10 and 128x2)\nFAILED tests/test_evaluator.py::test_evaluator_metrics - RuntimeError: mat1 and mat2 shapes cannot be multiplied (32x10 and 128x2)\nFAILED tests/test_trainer.py::test_trainer_loop - RuntimeError: mat1 and mat2 shapes cannot be multiplied (32x10 and 128x2)\n========================= 3 failed, 1 passed in 1.45s ==========================\n`,
		"data/metrics.csv": `epoch,loss,accuracy,val_loss,val_accuracy
1,0.69,0.52,0.68,0.51
2,12.4,0.49,25.1,0.48
3,45.2,0.46,89.4,0.45
4,102.3,0.44,210.4,0.43
5,312.4,0.42,512.3,0.41
6,812.3,0.40,1024.5,0.40
7,1512.4,0.39,2104.3,0.39
8,2512.4,0.38,3204.3,0.38
9,3812.4,0.37,4504.3,0.37
10,5112.4,0.36,6204.3,0.36
`,
	}

	for relPath, content := range files {
		fullPath := filepath.Join(dir, relPath)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	return dir
}

func scaffoldRustService(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"Cargo.toml": `[package]
name = "rust_service"
version = "0.1.0"
edition = "2021"

[dependencies]
prost = "0.12"
tokio = { version = "1", features = ["full"] }
tonic = "0.10"
`,
		"build.rs": `fn main() {
    tonic_build::compile_protos("proto/user.proto").unwrap();
}`,
		"proto/user.proto": `syntax = "proto3";
package user;
message User {
  // BUG: user_id is uint32 here but i64 in Rust struct
  uint32 user_id = 1;
  string name = 2;
}`,
		"proto/product.proto": `syntax = "proto3";
package product;
message Product {
  string id = 1;
}`,
		"src/main.rs": `mod handlers;
mod models;
mod db;
fn main() { 
    println!("Server starting...");
    let mut count = 0;
    count += 1;
}`,
		"src/lib.rs": `pub mod handlers; pub mod models; pub mod db;`,
		"src/handlers/user_handler.rs": `pub fn get_user() {}
pub fn create_user() {}
pub fn update_user() {}
pub fn delete_user() {}
pub fn handle_user() {}

pub fn test_get_user_handler() -> Result<(), String> {
    let res: Result<(), String> = Err("ParseError(\"invalid user ID format\")".to_string());
    res.unwrap();
    Ok(())
}
`,
		"src/handlers/product_handler.rs": `pub struct ProductHandler {
    pub name: String,
}
pub fn get_product() {}
pub fn create_product() {}
pub fn delete_product() {}
pub fn handle_product() {}`,
		"src/handlers/mod.rs": `pub mod user_handler; pub mod product_handler;
fn validate_input(input: &str) -> bool {
    input.len() > 0
}`,
		"src/models/user.rs": `use std::collections::HashMap;

#[derive(Debug)]
pub struct User {
    // BUG: i64 doesn't match uint32 in proto
    pub user_id: i64,
    pub name: String,
    updated_at: String,
}

enum userRole {
    Admin,
    User
}
`,
		"src/models/product.rs": `pub struct Product { 
    pub id: String,
    pub price: f64,
}
impl Product {
    pub fn new(id: String) -> Self {
        Product { id, price: 0.0 }
    }
}
`,
		"src/models/mod.rs": `pub mod user; pub mod product;`,
		"src/db/connection.rs": `pub fn connect() {
    let mut conn_string = String::from("postgres://localhost");
    println!("Connecting to {}", conn_string);
}`,
		"src/db/queries.rs": `use std::sync::Arc;
pub fn get_user_query(id: i64) -> String {
    format!("SELECT * FROM users WHERE id = {}", id)
}`,
		"src/db/mod.rs": `pub mod connection; pub mod queries;
struct DbError;
type Result<T> = std::result::Result<T, DbError>;`,
		"tests/integration_test.rs": `#[test] 
fn test_user_serialization() { 
    let res: Result<(), String> = Err("EncodeError(\"failed to encode Protobuf message: type mismatch: field user_id expected uint32, found i64\")".to_string());
    res.unwrap();
}`,
		"README.md": `# Rust gRPC Service
Known issues:
- Protobuf serialization is failing for the User message.
- There is a type mismatch somewhere between the .proto definition and the Rust struct definition for user_id.`,
		"logs/cargo_build.log": `warning: unused import: 'std::collections::HashMap'\n  --> src/models/user.rs:2:5\n   |\n 2 | use std::collections::HashMap;\n   |     ^^^^^^^^^^^^^^^^^^^^^^^^^\n   |\n   = note: '#[warn(unused_imports)]' on by default\n\nwarning: unused variable: 'user_id'\n  --> src/handlers/user_handler.rs:12:13\n   |\n12 |     let mut user_id = req.param(\"id\").unwrap();\n   |             ^^^^^^^ help: if this is intentional, prefix it with an underscore: '_user_id'\n   |\n   = note: '#[warn(unused_variables)]' on by default\n\nwarning: struct 'ProductHandler' is never constructed\n  --> src/handlers/product_handler.rs:5:12\n   |\n 5 | pub struct ProductHandler {\n   |            ^^^^^^^^^^^^^^\n   |\n   = note: '#[warn(dead_code)]' on by default\n\nwarning: function 'delete_product' is never used\n  --> src/handlers/product_handler.rs:18:8\n   |\n18 | pub fn delete_product() {}\n   |        ^^^^^^^^^^^^^^\n\nwarning: variable does not need to be mutable\n  --> src/db/connection.rs:14:9\n   |\n14 |     let mut conn_string = String::from(\"postgres://localhost\");\n   |         ----^^^^^^^^^^^\n   |         |\n   |         help: remove this 'mut'\n   |\n   = note: '#[warn(unused_mut)]' on by default\n\nwarning: unused import: 'std::sync::Arc'\n  --> src/db/queries.rs:1:5\n   |\n 1 | use std::sync::Arc;\n   |     ^^^^^^^^^^^^^^\n\nwarning: associated function 'new' is never used\n  --> src/models/product.rs:8:12\n   |\n 8 |     pub fn new(id: String) -> Self {\n   |            ^^^\n\nwarning: value assigned to 'count' is never read\n  --> src/main.rs:24:13\n   |\n24 |     let mut count = 0;\n   |             ^^^^^\n   |\n   = note: '#[warn(unused_assignments)]' on by default\n\nwarning: type 'UserRole' should have an upper camel case name\n  --> src/models/user.rs:18:10\n   |\n18 | enum userRole {\n   |      ^^^^^^^^ help: convert the identifier to upper camel case: 'UserRole'\n   |\n   = note: '#[warn(non_camel_case_types)]' on by default\n\nwarning: type alias is never used: 'Result'\n  --> src/db/mod.rs:3:1\n   |\n 3 | type Result<T> = std::result::Result<T, DbError>;\n   | ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n\nwarning: field 'updated_at' is never read\n  --> src/models/user.rs:30:5\n   |\n30 |     updated_at: String,\n   |     ^^^^^^^^^^^^^^^^^^\n   |\n   = note: 'structs' containing private fields that are never read are often a sign of dead code\n\nwarning: function 'validate_input' is never used\n  --> src/handlers/mod.rs:45:4\n   |\n45 | fn validate_input(input: &str) -> bool {\n   |    ^^^^^^^^^^^^^^\n\nwarning: 'rust_service' (bin \"rust_service\") generated 12 warnings\n`,
		"logs/cargo_test.log":  `running 3 tests\ntest models::user::test_user_creation ... ok\ntest handlers::user_handler::test_get_user_handler ... FAILED\ntest tests::integration_test::test_user_serialization ... FAILED\n\nfailures:\n\n---- handlers::user_handler::test_get_user_handler stdout ----\nthread 'handlers::user_handler::test_get_user_handler' panicked at 'called 'Result::unwrap()' on an 'Err' value: ParseError(\"invalid user ID format\")', src/handlers/user_handler.rs:45:32\nstack backtrace:\n   0: rust_begin_unwind\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panicking.rs:645:5\n   1: core::panicking::panic_fmt\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/panicking.rs:72:14\n   2: core::result::unwrap_failed\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/result.rs:1649:5\n   3: core::result::Result<T,E>::unwrap\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/result.rs:1073:23\n   4: rust_service::handlers::user_handler::test_get_user_handler\n             at ./src/handlers/user_handler.rs:45:9\n   5: rust_service::handlers::user_handler::test_get_user_handler::{{closure}}\n             at ./src/handlers/user_handler.rs:40:34\n   6: core::ops::function::FnOnce::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/ops/function.rs:250:5\n   7: core::ops::function::FnOnce::call_once{{vtable.shim}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/ops/function.rs:322:5\n   8: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n   9: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n  10: std::sys_common::backtrace::__rust_begin_short_backtrace\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/sys_common/backtrace.rs:134:18\n  11: std::thread::Builder::spawn_unchecked_::{{closure}}::{{closure}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/thread/mod.rs:529:17\n  12: <core::panic::unwind_safe::AssertUnwindSafe<F> as core::ops::function::FnOnce<()>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/panic/unwind_safe.rs:271:9\n  13: std::panicking::try::do_call\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panicking.rs:552:40\n  14: std::panicking::try\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panicking.rs:516:19\n  15: std::panic::catch_unwind\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panic.rs:142:14\n  16: std::thread::Builder::spawn_unchecked_::{{closure}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/thread/mod.rs:528:30\n  17: core::ops::function::FnOnce::call_once{{vtable.shim}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/ops/function.rs:322:5\n  18: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n  19: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n  20: std::sys::unix::thread::Thread::new::thread_start\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/sys/unix/thread.rs:108:17\n  21: start_thread\n             at ./nptl/pthread_create.c:442:8\n  22: clone3\n             at ./sysdeps/unix/sysv/linux/x86_64/clone3.S:81\nnote: Some details are omitted, run with 'RUST_BACKTRACE=full' for a verbose backtrace.\n\n---- tests::integration_test::test_user_serialization stdout ----\nthread 'tests::integration_test::test_user_serialization' panicked at 'called 'Result::unwrap()' on an 'Err' value: EncodeError(\"failed to encode Protobuf message: type mismatch: field user_id expected uint32, found i64\")', tests/integration_test.rs:15:45\nstack backtrace:\n   0: rust_begin_unwind\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panicking.rs:645:5\n   1: core::panicking::panic_fmt\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/panicking.rs:72:14\n   2: core::result::unwrap_failed\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/result.rs:1649:5\n   3: core::result::Result<T,E>::unwrap\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/result.rs:1073:23\n   4: integration_test::test_user_serialization\n             at ./tests/integration_test.rs:15:9\n   5: integration_test::test_user_serialization::{{closure}}\n             at ./tests/integration_test.rs:10:34\n   6: core::ops::function::FnOnce::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/ops/function.rs:250:5\n   7: core::ops::function::FnOnce::call_once{{vtable.shim}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/ops/function.rs:322:5\n   8: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n   9: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n  10: std::sys_common::backtrace::__rust_begin_short_backtrace\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/sys_common/backtrace.rs:134:18\n  11: std::thread::Builder::spawn_unchecked_::{{closure}}::{{closure}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/thread/mod.rs:529:17\n  12: <core::panic::unwind_safe::AssertUnwindSafe<F> as core::ops::function::FnOnce<()>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/panic/unwind_safe.rs:271:9\n  13: std::panicking::try::do_call\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panicking.rs:552:40\n  14: std::panicking::try\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panicking.rs:516:19\n  15: std::panic::catch_unwind\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/panic.rs:142:14\n  16: std::thread::Builder::spawn_unchecked_::{{closure}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/thread/mod.rs:528:30\n  17: core::ops::function::FnOnce::call_once{{vtable.shim}}\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/core/src/ops/function.rs:322:5\n  18: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n  19: <alloc::boxed::Box<F,A> as core::ops::function::FnOnce<Args>>::call_once\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/alloc/src/boxed.rs:2007:9\n  20: std::sys::unix::thread::Thread::new::thread_start\n             at /rustc/8460ca823e8367a30dda433f814e1fa9656d0d21/library/std/src/sys/unix/thread.rs:108:17\n  21: start_thread\n             at ./nptl/pthread_create.c:442:8\n  22: clone3\n             at ./sysdeps/unix/sysv/linux/x86_64/clone3.S:81\n\nfailures:\n    handlers::user_handler::test_get_user_handler\n    tests::integration_test::test_user_serialization\n\ntest result: FAILED. 1 passed; 2 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.05s\n`,
	}

	for relPath, content := range files {
		fullPath := filepath.Join(dir, relPath)
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		os.WriteFile(fullPath, []byte(content), 0644)
	}

	return dir
}

type scenarioResult struct {
	Name     string    `json:"name"`
	Baseline RunResult `json:"baseline"`
	Hooked   RunResult `json:"hooked"`
	FullTzro RunResult `json:"full_tzro"`
}

func TestPiCoderE2E_Scenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E benchmark in short mode")
	}

	apiKey, model := loadEnv(t)
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set in .env — skipping E2E benchmark")
	}

	tzroBin := buildTzroBinary(t)

	scenarios := []struct {
		name     string
		scaffold func(t *testing.T) string
		exts     []string
		signals  []QualitySignal
	}{
		{"TS_Monorepo", scaffoldTSMonorepo, []string{"*.ts", "*.tsx"}, tsMonorepoSignals},
		{"Python_ML", scaffoldPythonMLPipeline, []string{"*.py"}, pythonMLSignals},
		{"Rust_Service", scaffoldRustService, []string{"*.rs"}, rustServiceSignals},
	}

	var allResults []scenarioResult
	var totalCost float64

	pct := func(base, new int) float64 {
		if base == 0 {
			return 0
		}
		return float64(new-base) / float64(base) * 100
	}
	pctF := func(base, new float64) float64 {
		if base == 0 {
			return 0
		}
		return (new - base) / base * 100
	}
	pctMs := func(base, new int64) float64 {
		if base == 0 {
			return 0
		}
		return float64(new-base) / float64(base) * 100
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			if totalCost > 4.00 {
				t.Fatalf("Total cost exceeded $4.00 safety limit (current: $%.2f)", totalCost)
			}

			t.Logf("\n═══════════════════════════════════════════════════════")
			t.Logf("  SCENARIO: %s", sc.name)
			t.Logf("═══════════════════════════════════════════════════════")

			ws := sc.scaffold(t)

			// Pre-index with tzro skeleton
			filepath.Walk(ws, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				for _, ext := range sc.exts {
					matched, _ := filepath.Match(ext, info.Name())
					if matched {
						cmd := exec.Command(tzroBin, "skeleton", path)
						cmd.Dir = ws
						cmd.CombinedOutput()
						break
					}
				}
				return nil
			})

			resBase := agentLoop(t, apiKey, model, ws, false)
			totalCost += resBase.TotalCostUSD

			resHook := agentLoop(t, apiKey, model, ws, true)
			totalCost += resHook.TotalCostUSD

			resTzro := func() RunResult {
				// Build context pack using graph executor + GLiNER
				pack := buildContextPack(t, tzroBin, ws, sc.exts)
				return agentLoopWithTzroAndPack(t, apiKey, model, tzroBin, ws, &pack)
			}()
			totalCost += resTzro.TotalCostUSD

			allResults = append(allResults, scenarioResult{
				Name:     sc.name,
				Baseline: resBase,
				Hooked:   resHook,
				FullTzro: resTzro,
			})

			// Print the 3-way comparison table
			t.Logf("\n=======================================================================================================")
			t.Logf("│ Mode         │ Prompt Tok │ Compl Tok  │ Tool Out (B) │    Cost ($)  │ Wall (s) │ Turns │")
			t.Logf("├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────┼───────┤")
			t.Logf("│ Baseline     │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │",
				resBase.PromptTokens, resBase.CompletionTokens, resBase.ToolOutputBytes, resBase.TotalCostUSD, float64(resBase.WallClockMs)/1000, resBase.Turns)
			t.Logf("│ Hooked       │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │",
				resHook.PromptTokens, resHook.CompletionTokens, resHook.ToolOutputBytes, resHook.TotalCostUSD, float64(resHook.WallClockMs)/1000, resHook.Turns)
			t.Logf("│ Full Tzro    │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │",
				resTzro.PromptTokens, resTzro.CompletionTokens, resTzro.ToolOutputBytes, resTzro.TotalCostUSD, float64(resTzro.WallClockMs)/1000, resTzro.Turns)
			t.Logf("├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────┼───────┤")
			t.Logf("│ Hooked Δ     │ %9.1f%% │          — │ %11.1f%% │ %11.1f%% │ %7.1f%% │     — │",
				pct(resBase.PromptTokens, resHook.PromptTokens),
				pct(resBase.ToolOutputBytes, resHook.ToolOutputBytes),
				pctF(resBase.TotalCostUSD, resHook.TotalCostUSD),
				pctMs(resBase.WallClockMs, resHook.WallClockMs))
			t.Logf("│ Full Tzro Δ  │ %9.1f%% │          — │ %11.1f%% │ %11.1f%% │ %7.1f%% │     — │",
				pct(resBase.PromptTokens, resTzro.PromptTokens),
				pct(resBase.ToolOutputBytes, resTzro.ToolOutputBytes),
				pctF(resBase.TotalCostUSD, resTzro.TotalCostUSD),
				pctMs(resBase.WallClockMs, resTzro.WallClockMs))
			t.Logf("└──────────────┴────────────┴────────────┴──────────────┴──────────────┴──────────┴───────┘")

			// --- Quality scoring ---
			qBase := scoreQuality(resBase.FinalAnswer, sc.signals)
			qHook := scoreQuality(resHook.FinalAnswer, sc.signals)
			qTzro := scoreQuality(resTzro.FinalAnswer, sc.signals)

			logQualityComparison(t, sc.name, sc.signals, qBase, qHook, qTzro)

			// Warn if Full Tzro found fewer bugs than baseline
			if qTzro.Found < qBase.Found {
				t.Logf("⚠️  WARNING: Full Tzro found %d/%d bugs vs Baseline %d/%d — quality regression!",
					qTzro.Found, qTzro.Total, qBase.Found, qBase.Total)
			} else if qTzro.Found >= qBase.Found {
				t.Logf("✓ Full Tzro found %d/%d bugs (Baseline: %d/%d) — quality preserved or improved",
					qTzro.Found, qTzro.Total, qBase.Found, qBase.Total)
			}
		})
	}

	// Write combined results
	outPath := filepath.Join("testdata", "scenarios_benchmark_results.json")
	os.MkdirAll(filepath.Dir(outPath), 0755)
	b, _ := json.MarshalIndent(allResults, "", "  ")
	os.WriteFile(outPath, b, 0644)
}
